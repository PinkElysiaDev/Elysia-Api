package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var definitionIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// EvaluationContext contains gateway-owned metadata. Scope is supplied by the
// selected model/account binding and cannot be changed by a mapping expression.
type EvaluationContext struct {
	Scope  Scope
	Values Object
	// ResolveRequestScope is supplied by the gateway after model/authorization
	// lookup. Definitions cannot execute it or replace its returned binding.
	ResolveRequestScope func(*Request) (Scope, error)
}

// Module is a registered, thread-safe adapter implementation. Definitions can
// reference modules but cannot register executable code.
type Module interface {
	Name() string
	Directions() []Direction
	Convert(context.Context, Direction, Value, EvaluationContext) (Value, error)
}

// Compiler owns the installed engine modules and feature catalog. Compiled
// revisions copy configuration and can be shared by concurrent requests.
type Compiler struct {
	limits   Limits
	modules  map[string]Module
	features map[string]bool
}

// NewCompiler validates engine limits and installed module identities. Feature
// flags describe implemented engine support, never user-requested promises.
func NewCompiler(limits Limits, modules []Module, features []string) (*Compiler, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	compiler := &Compiler{limits: limits, modules: make(map[string]Module), features: map[string]bool{"mapping.v2": true, "transport.http_json": true, "transport.sse": true, "transport.ndjson": true}}
	for _, module := range modules {
		if module == nil || !definitionIdentifier.MatchString(module.Name()) {
			return nil, fmt.Errorf("invalid module identity")
		}
		if _, exists := compiler.modules[module.Name()]; exists {
			return nil, fmt.Errorf("duplicate module %q", module.Name())
		}
		compiler.modules[module.Name()] = module
	}
	for _, feature := range features {
		compiler.features[feature] = true
	}
	return compiler, nil
}

type compiledMapping struct {
	capabilities  CapabilitySet
	module        Module
	transform     *compiledExpression
	after         *compiledExpression
	input, output *ValueSchema
	rules         []compiledEventRule
}

type compiledEventRule struct{ when, emit *compiledExpression }

// Compiled is an immutable protocol revision; mutable event state belongs to a
// request/session, never to this registry object.
type Compiled struct {
	identity     Identity
	definition   Value
	hash         string
	samplesHash  string
	limits       Limits
	capabilities CapabilitySet
	native       NativePolicy
	mappings     map[Direction]compiledMapping
	operations   map[string]Operation
	taskMappings map[string]map[string]compiledMapping
}

// Compile strictly parses one v2 definition and compiles every direction.
// Successful compilation alone does not authorize activation; verification is
// a separate service that binds evidence to Hash and CompilerVersion.
func (compiler *Compiler) Compile(raw []byte) (*Compiled, []ConversionIssue) {
	if len(raw) > compiler.limits.BufferBytes {
		return nil, definitionIssues(Identity{}, "/", fmt.Errorf("definition exceeds buffer limit"))
	}
	value, err := ParseValue(raw)
	if err != nil {
		return nil, definitionIssues(Identity{}, "/", err)
	}
	if err := checkValueLimits(value, compiler.limits); err != nil {
		return nil, definitionIssues(Identity{}, "/", err)
	}
	var definition Definition
	if err := decodeContract(raw, &definition); err != nil {
		return nil, definitionIssues(Identity{}, "/", err)
	}
	identity := definition.Identity()
	fail := func(path string, err error) (*Compiled, []ConversionIssue) {
		return nil, definitionIssues(identity, path, err)
	}
	if err := compiler.checkDefinition(definition); err != nil {
		return fail("/", err)
	}
	limits := compiler.limits
	if definition.Limits != nil {
		limits = *definition.Limits
	}
	compiled := &Compiled{identity: identity, definition: value, limits: limits, native: definition.Native, capabilities: make(CapabilitySet), mappings: make(map[Direction]compiledMapping), operations: definition.Operations}
	for capability, supported := range definition.Capabilities {
		compiled.capabilities[capability] = supported
	}
	expressions := &expressionCompiler{limits: limits, references: definition.Expressions, resolving: make(map[string]bool), used: make(map[string]bool)}
	for _, direction := range DirectionCatalog() {
		mapping, exists := definition.Directions[direction]
		if !exists {
			continue
		}
		path := "/directions/" + string(direction)
		entry, err := compiler.compileMapping(mapping, path, direction, expressions)
		if err != nil {
			return fail(path, err)
		}
		entry.capabilities = compiled.capabilities
		if mapping.Capabilities != nil {
			entry.capabilities = make(CapabilitySet)
			for capability, supported := range mapping.Capabilities {
				if !definition.Capabilities[capability] {
					return fail(path+"/capabilities", fmt.Errorf("direction capability %q is not declared by this definition", capability))
				}
				entry.capabilities[capability] = supported
			}
		}
		compiled.mappings[direction] = entry
	}
	if err := compiler.compileTaskMappings(compiled, definition, expressions); err != nil {
		return fail("/operations", err)
	}
	for _, reference := range sortedKeys(definition.Expressions) {
		if !expressions.used[reference] {
			return fail("/expressions/"+reference, fmt.Errorf("unreachable named expression"))
		}
	}
	canonical, err := EncodeValue(definition)
	if err != nil {
		return fail("/", err)
	}
	compiled.hash = hashValue(canonical)
	samples, err := EncodeValue(struct {
		Samples  []Sample        `json:"samples"`
		Sessions []SessionSample `json:"sessions"`
		Tasks    []TaskSample    `json:"tasks"`
	}{definition.Samples, definition.SessionSamples, definition.TaskSamples})
	if err != nil {
		return fail("/samples", err)
	}
	compiled.samplesHash = hashValue(samples)
	return compiled, nil
}

func (compiler *Compiler) compileMapping(mapping Mapping, path string, direction Direction, expressions *expressionCompiler) (compiledMapping, error) {
	entry := compiledMapping{input: mapping.Input, output: mapping.Output}
	if err := validateSchema(mapping.Input, path+"/input", 1, expressions.limits); err != nil {
		return entry, err
	}
	if err := validateSchema(mapping.Output, path+"/output", 1, expressions.limits); err != nil {
		return entry, err
	}
	choices := 0
	if mapping.Module != "" {
		choices++
	}
	if mapping.Transform != nil {
		choices++
	}
	if len(mapping.Rules) > 0 {
		choices++
	}
	if choices != 1 {
		return entry, fmt.Errorf("exactly one of module, transform or rules is required")
	}
	if mapping.Module != "" {
		module, exists := compiler.modules[mapping.Module]
		if !exists || !slices.Contains(module.Directions(), direction) {
			return entry, fmt.Errorf("module %q does not implement %s", mapping.Module, direction)
		}
		entry.module = module
	}
	if mapping.After != nil {
		if mapping.Module == "" {
			return entry, fmt.Errorf("after requires an installed module")
		}
		after, err := expressions.compile(*mapping.After, path+"/after", expressionScope{}, 1)
		if err != nil {
			return entry, err
		}
		if err := checkExpressionOutput(after, mapping.Output, expressions.limits); err != nil {
			return entry, err
		}
		entry.after = after
	}
	scope := expressionScope{input: mapping.Input}
	if mapping.Transform != nil {
		expression, err := expressions.compile(*mapping.Transform, path+"/transform", scope, 1)
		if err != nil {
			return entry, err
		}
		if err := checkExpressionOutput(expression, mapping.Output, expressions.limits); err != nil {
			return entry, err
		}
		entry.transform = expression
	}
	if len(mapping.Rules) > 0 {
		if !isEventDirection(direction) {
			return entry, fmt.Errorf("event rules require an event direction")
		}
		if mapping.UnknownEvent != "reject" {
			return entry, fmt.Errorf("rules require unknownEvent: reject; declare ignorable frames with an explicit empty-array rule")
		}
		for index, rule := range mapping.Rules {
			rulePath := fmt.Sprintf("%s/rules/%d", path, index)
			when, err := expressions.compile(rule.When, rulePath+"/when", scope, 1)
			if err != nil {
				return entry, err
			}
			if !isAssignable(when.result, BooleanType) {
				return entry, fmt.Errorf("%s condition must be boolean", rulePath)
			}
			emit, err := expressions.compile(rule.Emit, rulePath+"/emit", scope, 1)
			if err != nil {
				return entry, err
			}
			entry.rules = append(entry.rules, compiledEventRule{when: when, emit: emit})
		}
	} else if mapping.UnknownEvent != "" {
		return entry, fmt.Errorf("unknownEvent requires event rules")
	}
	return entry, nil
}

func (compiler *Compiler) checkDefinition(definition Definition) error {
	if definition.SchemaVersion != DefinitionSchemaVersion {
		return fmt.Errorf("schemaVersion must be %d", DefinitionSchemaVersion)
	}
	if !definitionIdentifier.MatchString(definition.ID) || strings.TrimSpace(definition.Version) == "" || strings.TrimSpace(definition.Family) == "" || strings.TrimSpace(definition.WireVersion) == "" {
		return fmt.Errorf("id, version, family and wireVersion are required")
	}
	if len(definition.Directions) == 0 || len(definition.Operations) == 0 {
		return fmt.Errorf("directions and operations must not be empty")
	}
	for path, key := range definition.Native.ArrayKeys {
		if !definition.Native.Preserve {
			return fmt.Errorf("native arrayKeys requires preserve")
		}
		if _, err := parsePointer(path); err != nil {
			return fmt.Errorf("native array path: %w", err)
		}
		if key == "" {
			return fmt.Errorf("native array identity must select an item field")
		}
		if _, err := parsePointer(key); err != nil {
			return fmt.Errorf("native array identity: %w", err)
		}
	}
	if definition.Native.Preserve {
		for _, pair := range [][2]Direction{{DecodeRequest, EncodeRequest}, {DecodeResponse, EncodeResponse}, {DecodeEvent, EncodeEvent}, {DecodeClientEvent, EncodeUpstreamEvent}} {
			_, hasDecoder := definition.Directions[pair[0]]
			_, hasEncoder := definition.Directions[pair[1]]
			if hasEncoder && !hasDecoder {
				return fmt.Errorf("native preservation for %s requires %s", pair[1], pair[0])
			}
		}
	}
	for _, required := range definition.Requires {
		if !compiler.features[required] {
			return fmt.Errorf("required engine feature %q is unavailable", required)
		}
	}
	for capability := range definition.Capabilities {
		if !slices.Contains(CapabilityCatalog(), capability) {
			return fmt.Errorf("unknown capability %q", capability)
		}
	}
	for direction := range definition.Directions {
		if !slices.Contains(DirectionCatalog(), direction) {
			return fmt.Errorf("unknown direction %q", direction)
		}
	}
	if definition.Limits != nil {
		limits := *definition.Limits
		if err := limits.Validate(); err != nil {
			return err
		}
		if limits.Depth > compiler.limits.Depth || limits.Nodes > compiler.limits.Nodes || limits.Mutations > compiler.limits.Mutations || limits.StateItems > compiler.limits.StateItems || limits.BufferBytes > compiler.limits.BufferBytes {
			return fmt.Errorf("definition limits cannot exceed engine limits")
		}
	}
	for _, name := range sortedKeys(definition.Operations) {
		if err := compiler.checkOperation(name, definition.Operations[name], definition); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	if len(definition.Samples)+len(definition.SessionSamples)+len(definition.TaskSamples) > compiler.limits.StateItems {
		return fmt.Errorf("sample count exceeds engine fixture limit")
	}
	for _, sample := range definition.Samples {
		if sample.ID == "" || seen[sample.ID] {
			return fmt.Errorf("samples require unique nonempty IDs")
		}
		seen[sample.ID] = true
		if _, exists := definition.Directions[sample.Direction]; !exists {
			return fmt.Errorf("sample %q references an unimplemented direction", sample.ID)
		}
		if sample.Input.IsZero() || (sample.Expected.IsZero() == (sample.ExpectedIssue == "")) {
			return fmt.Errorf("sample %q requires input and expected output or issue", sample.ID)
		}
		if sample.Sequence && !isEventDirection(sample.Direction) {
			return fmt.Errorf("sample %q sequence requires an event direction", sample.ID)
		}
		for _, capability := range sample.Capabilities {
			if !definition.Capabilities[capability] {
				return fmt.Errorf("sample %q covers an undeclared capability", sample.ID)
			}
		}
	}
	for _, sample := range definition.SessionSamples {
		if sample.ID == "" || seen[sample.ID] {
			return fmt.Errorf("session samples require unique nonempty IDs")
		}
		seen[sample.ID] = true
		operation, exists := definition.Operations[sample.Operation]
		if !exists || operation.Transport != WebSocket {
			return fmt.Errorf("session sample %q requires a WebSocket operation", sample.ID)
		}
		if len(sample.Steps) == 0 || len(sample.Steps) > compiler.limits.StateItems {
			return fmt.Errorf("session sample %q has an invalid step count", sample.ID)
		}
		for _, step := range sample.Steps {
			if !isEventDirection(step.Direction) || !hasDefinitionDirections(definition, step.Direction) || step.Input.IsZero() || step.Expected.IsZero() {
				return fmt.Errorf("session sample %q requires implemented event directions, inputs and expectations", sample.ID)
			}
		}
	}
	return nil
}

func (compiler *Compiler) checkOperation(name string, operation Operation, definition Definition) error {
	if !definitionIdentifier.MatchString(name) {
		return fmt.Errorf("invalid operation name %q", name)
	}
	if !slices.Contains(OperationKindCatalog(), operation.Kind) {
		return fmt.Errorf("operation %q has unsupported kind", name)
	}
	if !slices.Contains([]string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodPatch}, operation.Method) {
		return fmt.Errorf("operation %q has unsupported method", name)
	}
	path, err := url.Parse(operation.Path)
	if err != nil || path.IsAbs() || path.Host != "" || !strings.HasPrefix(operation.Path, "/") || strings.HasPrefix(operation.Path, "//") || path.Fragment != "" || strings.Contains(operation.Path, "\\") {
		return fmt.Errorf("operation %q requires a relative absolute-path endpoint", name)
	}
	if !slices.Contains(TransportCatalog(), operation.Transport) {
		return fmt.Errorf("operation %q has unknown transport", name)
	}
	limits := compiler.limits
	if definition.Limits != nil {
		limits = *definition.Limits
	}
	if err := checkSessionOperation(operation, definition, limits); err != nil {
		return fmt.Errorf("operation %q: %w", name, err)
	}
	if operation.Transport == WebSocket && !compiler.features["transport.websocket"] {
		return fmt.Errorf("WebSocket transport is not installed")
	}
	if operation.Kind == "submit" && !compiler.features["tasks.async"] {
		return fmt.Errorf("asynchronous task transport is not installed")
	}
	for _, direction := range []Direction{operation.Request, operation.Response} {
		if direction != "" {
			if _, exists := definition.Directions[direction]; !exists {
				return fmt.Errorf("operation %q references missing direction %q", name, direction)
			}
		}
	}
	if operation.Transport == SSE || operation.Transport == NDJSON || operation.Transport == WebSocket {
		_, canDecode := definition.Directions[DecodeEvent]
		_, canEncode := definition.Directions[EncodeEvent]
		if !canDecode && !canEncode {
			return fmt.Errorf("operation %q requires an event direction", name)
		}
	}
	if operation.Auth.Location != "none" && operation.Auth.Location != "header" && operation.Auth.Location != "query" {
		return fmt.Errorf("operation %q requires auth location none, header or query", name)
	}
	if operation.Auth.Location != "none" && strings.TrimSpace(operation.Auth.Name) == "" {
		return fmt.Errorf("operation %q requires an auth field name", name)
	}
	if operation.Framing != nil && strings.ContainsAny(operation.Framing.EventName, "\r\n") {
		return fmt.Errorf("operation %q has an invalid SSE event name", name)
	}
	if operation.Request != "" && operation.Request != DecodeRequest && operation.Request != EncodeRequest {
		return fmt.Errorf("operation %q request must select a request direction", name)
	}
	if operation.Response != "" && operation.Response != DecodeResponse && operation.Response != EncodeResponse && operation.Response != DecodeEvent && operation.Response != EncodeEvent {
		return fmt.Errorf("operation %q response must select a response/event direction", name)
	}
	for key, value := range operation.Headers {
		if strings.ContainsAny(key+value, "\r\n") || strings.Contains(key, ":") {
			return fmt.Errorf("operation %q has invalid HTTP headers", name)
		}
	}
	if strings.ContainsAny(operation.Auth.Name+operation.Auth.Prefix, "\r\n") {
		return fmt.Errorf("operation %q has invalid credential injection", name)
	}
	if operation.Task != nil {
		if operation.Kind != "submit" {
			return fmt.Errorf("operation %q has a task flow without submit kind", name)
		}
		for _, reference := range []string{operation.Task.Status, operation.Task.Result, operation.Task.Cancel} {
			if reference != "" {
				if _, exists := definition.Operations[reference]; !exists {
					return fmt.Errorf("operation %q has an unknown task reference", name)
				}
			}
		}
	}
	return nil
}

// Identity returns the immutable revision's semantic wire identity.
func (compiled *Compiled) Identity() Identity { return compiled.identity }

// Hash identifies the normalized definition including samples and metadata.
func (compiled *Compiled) Hash() string { return compiled.hash }

// SamplesHash identifies the exact offline fixture collection.
func (compiled *Compiled) SamplesHash() string { return compiled.samplesHash }

// Operations copies compiled transport metadata without reparsing expressions
// or fixtures. Callers may customize their copy for one pinned request.
func (compiled *Compiled) Operations() map[string]Operation {
	operations := make(map[string]Operation, len(compiled.operations))
	for name, operation := range compiled.operations {
		operation.Headers, operation.Query = maps.Clone(operation.Headers), maps.Clone(operation.Query)
		if operation.Framing != nil {
			framing := *operation.Framing
			framing.Done = append([]string(nil), framing.Done...)
			operation.Framing = &framing
		}
		if operation.Task != nil {
			task := *operation.Task
			// Deep copy expressions so callers cannot mutate the pinned revision.
			value, _ := EncodeValue(task)
			_ = value.Decode(&task)
			operation.Task = &task
		}
		if operation.Session != nil {
			session := *operation.Session
			session.QueryFields = append([]string(nil), session.QueryFields...)
			session.HeaderFields = append([]string(nil), session.HeaderFields...)
			if session.InputMedia != nil {
				media := *session.InputMedia
				session.InputMedia = &media
			}
			if session.OutputMedia != nil {
				media := *session.OutputMedia
				session.OutputMedia = &media
			}
			operation.Session = &session
		}
		operations[name] = operation
	}
	return operations
}

// Definition returns an independent copy suitable for editing or persistence.
func (compiled *Compiled) Definition() Definition {
	var definition Definition
	_ = compiled.definition.Decode(&definition)
	return definition
}

// Supports reports whether this revision implements an independent direction.
func (compiled *Compiled) Supports(direction Direction) bool {
	_, exists := compiled.mappings[direction]
	return exists
}

// Capabilities returns a copy of a direction's declared semantic features.
func (compiled *Compiled) Capabilities(direction Direction) CapabilitySet {
	result := CapabilitySet{}
	for capability, supported := range compiled.mappings[direction].capabilities {
		result[capability] = supported
	}
	return result
}

// Execute evaluates a compiled mapping with finite resources and structured
// diagnostics. It neither performs I/O nor silently invents missing fields.
func (compiled *Compiled) Execute(ctx context.Context, direction Direction, input Value, options EvaluationContext) (Value, []ConversionIssue) {
	fail := func(err error) (Value, []ConversionIssue) {
		var conversion *ConversionError
		if errors.As(err, &conversion) {
			return Value{}, append([]ConversionIssue(nil), conversion.Issues...)
		}
		code := InvalidInput
		if direction == DecodeResponse || direction == DecodeEvent {
			code = UpstreamContractViolation
		}
		return Value{}, []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "evaluate", Path: mappingErrorPath("/directions/"+string(direction), err), Reason: err.Error(), Suggestion: "Correct the input or declared mapping and verify the revision again."}}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	mapping, exists := compiled.mappings[direction]
	if !exists {
		return fail(fmt.Errorf("direction is not implemented"))
	}
	if err := checkValueLimits(input, compiled.limits); err != nil {
		return fail(err)
	}
	if err := checkValueSchema(input, mapping.input, "/input", 1, compiled.limits); err != nil {
		return fail(err)
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	state := evaluation{ctx: ctx, input: input, root: input, context: contextValue, budget: &evaluationBudget{limits: compiled.limits}}
	var output Value
	switch {
	case mapping.module != nil:
		output, err = mapping.module.Convert(ctx, direction, input, options)
	case mapping.transform != nil:
		output, err = mapping.transform.evaluate(state)
	default:
		output, err = evaluateEventRules(mapping, state)
	}
	if err != nil {
		return fail(err)
	}
	if mapping.after != nil {
		state.input = output
		output, err = mapping.after.evaluate(state)
		if err != nil {
			return fail(err)
		}
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkValueLimits(output, compiled.limits); err != nil {
		return fail(err)
	}
	if err := checkValueSchema(output, mapping.output, "/output", 1, compiled.limits); err != nil {
		return fail(err)
	}
	return output, nil
}

func evaluateEventRules(mapping compiledMapping, state evaluation) (Value, error) {
	for _, rule := range mapping.rules {
		value, err := rule.when.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		isMatch, err := readBoolean(value)
		if err != nil {
			return Value{}, err
		}
		if isMatch {
			return rule.emit.evaluate(state)
		}
	}
	return Value{}, fmt.Errorf("wire event has no declared mapping rule")
}

func definitionIssues(identity Identity, path string, err error) []ConversionIssue {
	return []ConversionIssue{{Code: InvalidDefinition, Severity: SeverityError, Protocol: identity, Stage: "compile", Path: mappingErrorPath(path, err), Reason: err.Error(), Suggestion: "Correct the definition at this location before verification."}}
}
func hashValue(value Value) string {
	sum := sha256.Sum256(value.Bytes())
	return hex.EncodeToString(sum[:])
}
