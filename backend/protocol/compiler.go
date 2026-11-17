package protocol

import (
	"context"
	"crypto/sha256"
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
	conversion          *streamConversionBinding
	observedNativeFrame Value
	InterleavedThinking bool
	ClientOutput        *ClientOutput
	Scope               Scope
	Values              Object
	State               *EvaluationState
	// Diagnostics collects non-blocking conversion issues for this request.
	// It is gateway-owned; mappings can report through it but cannot read it.
	Diagnostics        *DiagnosticSink
	identity           Identity
	compoundFrame      bool
	transportControl   *bool
	wireLimits         Limits
	valueReader        *valueReader
	inputView          *inputView
	continuationInput  *continuationInput
	continuation       *ContinuationBinding
	requestReplay      *requestReplay
	outputStream       *WireStreamValidation
	operation          *operationMetadata
	emissionOffset     int
	packageTransaction *packageStreamTransaction
	// ResolveRequestScope is supplied by the gateway after model/authorization
	// lookup. Definitions cannot execute it or replace its returned binding.
	ResolveRequestScope func(*Request) (Scope, error)
}

// Identity returns the immutable definition identity stamped by execution.
// Callers and mapping expressions cannot impersonate another wire revision.
func (options EvaluationContext) Identity() Identity { return options.identity }

// WireLimits exposes the current boundary to built-in native replay only.
func (options EvaluationContext) WireLimits() Limits {
	if options.wireLimits.BufferBytes == 0 {
		return DefaultLimits()
	}
	return options.wireLimits
}

// ConsumeTransportControl prevents a verified payload-free transport frame
// from being replayed as a generation event. Authored mappings cannot set it.
func (options EvaluationContext) ConsumeTransportControl() {
	if options.transportControl != nil {
		*options.transportControl = true
	}
}

func (options EvaluationContext) forDefinition(compiled *Compiled) EvaluationContext {
	options.identity = compiled.identity
	return options
}

// Compiler owns the declarative language and feature catalog. Compiled
// revisions copy configuration and can be shared by concurrent requests.
type Compiler struct {
	limits      Limits
	features    map[string]bool
	featureHash [32]byte
}

// 引擎 feature 键与默认已实现集合：feature 描述引擎自身能力，绝非用户承诺。
const (
	featureWebSocketTransport = "transport.websocket"
	featureAsyncTasks         = "tasks.async"
)

// operationMethodWhitelist 是声明式操作允许的 HTTP 方法集合。
var operationMethodWhitelist = []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodPatch}

var defaultFeatures = map[string]bool{
	"conversion.client_output.v1": true,
	ReasoningControlsFeature:      true,
	"conversion.policies.v1":      true,
	"conversion.continuation.v1":  true,
	"mapping.v2":                  true,
	"transport.http_json":         true,
	"transport.sse":               true,
	"transport.ndjson":            true,
}

// NewCompiler validates engine limits and the installed transport features.
// Features describe host support, never protocol-specific conversion behavior.
func NewCompiler(limits Limits, features []string) (*Compiler, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	compiler := &Compiler{limits: limits, features: maps.Clone(defaultFeatures)}
	for _, feature := range features {
		compiler.features[feature] = true
	}
	featureSet, err := EncodeValue(sortedKeys(compiler.features))
	if err != nil {
		return nil, err
	}
	compiler.featureHash = sha256.Sum256(featureSet.Bytes())
	return compiler, nil
}

type compiledMapping struct {
	preservesDynamicNative bool
	nativeWhen             *compiledOutputPlan
	finalCheck             *compiledOutputPlan
	deliveryRanges         []compiledDeliveryRange
	semanticRanges         []compiledDeliveryRange
	streamObserver         *compiledOutputPlan
	failureObservation     *compiledOutputPlan
	capabilities           CapabilitySet
	outputPlan             *compiledOutputPlan
	streamState            *compiledPackageState
	streamFinalizer        *compiledOutputPlan
	hasCoreInputPlan       bool
	hasCoreOutputPlan      bool
	input, output          *runtimeValueSchema
	frameBatch             bool
	isEventBatch           bool
	canDeferEvents         bool
	definitionHash         string
}

// Compiled is an immutable protocol revision; mutable event state belongs to a
// request/session, never to this registry object.
type Compiled struct {
	// The executable graph is immutable. This separately synchronized memo
	// stores only reproducible offline evidence, never request/runtime state.
	verification       *verificationMemo
	compileExpressions []*compiledExpression
	identity           Identity
	definition         Value
	hash               string
	executionHash      string
	samplesHash        string
	limits             Limits
	explicitLimits     bool
	capabilities       CapabilitySet
	native             NativePolicy
	mappings           map[Direction]compiledMapping
	wireContracts      map[Direction]compiledWireContract
	operations         map[string]Operation
	operationSchemas   map[string]*runtimeValueSchema
	modelPageValidator *runtimeValueSchema
	taskMappings       map[string]map[string]compiledMapping
	modelMappings      map[string]compiledMapping
	modelEncoders      map[string]compiledMapping
	agentMapping       *compiledMapping
	agentClientOutput  []compiledAgentOutputField
	agentEfforts       []string
	agentToolResults   InputKind
	packagePlan        *compiledPackagePlan
}

// Compile strictly parses one authored document and compiles every direction.
// Successful compilation alone does not authorize activation; verification is
// a separate service that binds evidence to Hash and CompilerVersion.
func (compiler *Compiler) Compile(raw []byte) (*Compiled, []ConversionIssue) {
	return compiler.CompilePackage(raw)
}

func checkSessionSamples(samples []SessionSample, operations map[string]Operation, supports func(Direction) bool, stepLimit int, seen map[string]bool) error {
	for _, sample := range samples {
		if sample.ID == "" || seen[sample.ID] {
			return fmt.Errorf("session samples require unique nonempty IDs")
		}
		seen[sample.ID] = true
		operation, exists := operations[sample.Operation]
		if !exists || operation.Transport != WebSocket {
			return fmt.Errorf("session sample %q requires a WebSocket operation", sample.ID)
		}
		if len(sample.Steps) == 0 || len(sample.Steps) > stepLimit {
			return fmt.Errorf("session sample %q has an invalid step count", sample.ID)
		}
		for _, step := range sample.Steps {
			if !isEventDirection(step.Direction) || !supports(step.Direction) || step.Input.IsZero() || step.Expected.IsZero() {
				return fmt.Errorf("session sample %q requires implemented event directions, inputs and expectations", sample.ID)
			}
		}
	}
	return nil
}

func checkOperationEndpoint(name string, operation Operation, limits Limits) error {
	if !definitionIdentifier.MatchString(name) {
		return fmt.Errorf("invalid operation name %q", name)
	}
	if !slices.Contains(OperationKindCatalog(), operation.Kind) {
		return fmt.Errorf("operation %q has unsupported kind", name)
	}
	if !slices.Contains(operationMethodWhitelist, operation.Method) {
		return fmt.Errorf("operation %q has unsupported method", name)
	}
	path, err := url.Parse(operation.Path)
	if err != nil || path.IsAbs() || path.Host != "" || !strings.HasPrefix(operation.Path, "/") || strings.HasPrefix(operation.Path, "//") || path.Fragment != "" || strings.Contains(operation.Path, "\\") {
		return fmt.Errorf("operation %q requires a relative absolute-path endpoint", name)
	}
	if !slices.Contains(TransportCatalog(), operation.Transport) {
		return fmt.Errorf("operation %q has unknown transport", name)
	}
	if err := validateSchema(operation.Input, "/operations/"+name+"/input", 1, limits); err != nil {
		return err
	}
	if operation.Input != nil && (operation.Kind != "generate" || operation.Transport == WebSocket) {
		return fmt.Errorf("operation %q input schema requires HTTP generation; task and session inputs use their own mappings", name)
	}
	return nil
}

func checkOperationFraming(name string, operation Operation) error {
	if operation.Auth.Location != "none" && operation.Auth.Location != "header" && operation.Auth.Location != "query" {
		return fmt.Errorf("operation %q requires auth location none, header or query", name)
	}
	if operation.Auth.Location != "none" && strings.TrimSpace(operation.Auth.Name) == "" {
		return fmt.Errorf("operation %q requires an auth field name", name)
	}
	if operation.Framing != nil && strings.ContainsAny(operation.Framing.EventName, "\r\n") {
		return fmt.Errorf("operation %q has an invalid SSE event name", name)
	}
	if framing := operation.Framing; framing != nil && framing.IdleMillis != 0 {
		if (operation.Transport != SSE && operation.Transport != NDJSON) || framing.IdleMillis < 0 || framing.IdleMillis > MaxStreamIdleMillis {
			return fmt.Errorf("operation %q idleMillis requires an HTTP stream and must be between 1 and %d", name, MaxStreamIdleMillis)
		}
	}
	if framing := operation.Framing; framing != nil && framing.EventNamePath != "" {
		if operation.Transport != SSE || framing.EventName != "" {
			return fmt.Errorf("operation %q must select one SSE event name source", name)
		}
		if _, err := parsePointer(framing.EventNamePath); err != nil {
			return fmt.Errorf("operation %q has an invalid event name pointer: %w", name, err)
		}
	}
	return nil
}

func checkOperationHeaders(name string, operation Operation) error {
	for key, value := range operation.Headers {
		if strings.ContainsAny(key+value, "\r\n") || strings.Contains(key, ":") {
			return fmt.Errorf("operation %q has invalid HTTP headers", name)
		}
	}
	if strings.ContainsAny(operation.Auth.Name+operation.Auth.Prefix, "\r\n") {
		return fmt.Errorf("operation %q has invalid credential injection", name)
	}
	return nil
}

// Identity returns the immutable revision's semantic wire identity.
func (compiled *Compiled) Identity() Identity { return compiled.identity }

// Hash identifies the normalized definition including samples and metadata.
func (compiled *Compiled) Hash() string { return compiled.hash }

// ExecutionHash distinguishes an operation's request programs within the same
// pinned package revision. Ordinary revisions use their complete package hash.
func (compiled *Compiled) ExecutionHash() string {
	if compiled.executionHash != "" {
		return compiled.executionHash
	}
	return compiled.hash
}

// ResourceLimits returns the immutable revision's bounded execution contract.
func (compiled *Compiled) ResourceLimits() Limits { return compiled.limits }

// SamplesHash identifies the exact offline fixture collection.
func (compiled *Compiled) SamplesHash() string { return compiled.samplesHash }

// Operations copies compiled transport metadata without reparsing expressions
// or fixtures. Transport overrides apply to the copy; input validation always
// uses the named operation's compiled schema.
func (compiled *Compiled) Operations() map[string]Operation {
	operations := make(map[string]Operation, len(compiled.operations))
	for name := range compiled.operations {
		operations[name], _ = compiled.Operation(name)
	}
	return operations
}

// OperationRoute exposes only immutable routing fields. Iteration never copies
// schemas, task expressions or mutable transport metadata for rejected choices.
type OperationRoute struct {
	Method, Path, Kind string
	Transport          Transport
}

func (compiled *Compiled) OperationRoutes() func(func(string, OperationRoute) bool) {
	return func(yield func(string, OperationRoute) bool) {
		for name, operation := range compiled.operations {
			if !yield(name, OperationRoute{operation.Method, operation.Path, operation.Kind, operation.Transport}) {
				return
			}
		}
	}
}

// Operation copies only the selected operation; callers own every mutable field.
func (compiled *Compiled) Operation(name string) (Operation, bool) {
	operation, exists := compiled.operations[name]
	if !exists {
		return Operation{}, false
	}
	operation.Headers, operation.Query = maps.Clone(operation.Headers), maps.Clone(operation.Query)
	if operation.Input != nil {
		var input ValueSchema
		value, _ := EncodeValue(operation.Input)
		_ = value.Decode(&input)
		operation.Input = &input
	}
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
	if operation.Models != nil {
		var discovery ModelDiscovery
		value, _ := EncodeValue(operation.Models)
		_ = value.Decode(&discovery)
		operation.Models = &discovery
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
	return operation, true
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
	output, err := compiled.executeMapping(ctx, direction, input, options)
	if err == nil {
		return output, nil
	}
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		return Value{}, conversion.Issues
	}
	code := ExecutionCancelled
	if errors.Is(err, context.DeadlineExceeded) {
		code = ExecutionTimeout
	}
	return Value{}, []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "evaluate", Path: "/", Reason: err.Error()}}
}

// mappingError shares the diagnostic path and original cause chain with
// the typed request boundary.
func (compiled *Compiled) mappingError(direction Direction, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	err = resourceIssue(err, compiled.identity, direction, "evaluate")
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		return err
	}
	var classified interface {
		error
		issue(Identity, Direction) ConversionIssue
	}
	if errors.As(err, &classified) {
		return &ConversionError{cause: err, Issues: []ConversionIssue{classified.issue(compiled.identity, direction)}}
	}
	code := InvalidInput
	if direction == DecodeResponse || direction == DecodeEvent {
		code = UpstreamContractViolation
	}
	return &ConversionError{cause: err, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "evaluate", Path: mappingErrorPath("/directions/"+string(direction), err), Reason: err.Error(), Suggestion: "Correct the input or declared mapping and verify the revision again."}}}
}

// executeMapping retains causes for runtime consumers; Execute exposes diagnostics
// for authoring callers whose API returns a report instead of a Go error.
func (compiled *Compiled) executeMapping(ctx context.Context, direction Direction, input Value, options EvaluationContext) (result Value, err error) {
	options, transaction, err := compiled.beginPackageTransaction(direction, options)
	if err != nil {
		return Value{}, compiled.mappingError(direction, err)
	}
	defer func() { transaction.finish(err) }()
	fail := func(err error) (Value, error) { return Value{}, compiled.mappingError(direction, err) }
	mapping, exists := compiled.mappings[direction]
	if !exists {
		return Value{}, compiled.rejectMissingMapping(ctx, direction)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	inputLimits, outputLimits, workLimits := compiled.mappingLimits(ctx, direction)
	if isPackageDecodeDirection(direction) {
		options = options.withInputView(input)
	}
	if err := options.inputView.checkResources(ctx, input, inputLimits, "mapping_input"); err != nil {
		return fail(err)
	}
	if err := mapping.input.validate(input, "/input", inputLimits); err != nil {
		return fail(err)
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	state := evaluation{ctx: ctx, input: input, root: input, context: contextValue, inputView: options.inputView, runtime: bindEvaluationRuntime(compiled, direction, options), streamSlots: options.packageTransaction.values(), streamTransaction: options.packageTransaction, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	var packageState *packageExecution

	if isPackageDecodeDirection(direction) {
		state.objectReader = state.inputView.objectReader()
	}
	packageState, err = compiled.preparePackageExecution(direction, state, inputLimits, options)
	if err != nil {
		return fail(err)
	}
	state.valueReader = packageState.reader
	state.capture = compiled.newNativeCapture(direction, options)
	state.native = compiled.newNativeEvaluation(ctx, direction, options)

	planned, err := mapping.outputPlan.build(state)
	if err != nil {
		return fail(err)
	}
	if err := options.packageTransaction.resolveDispositions(); err != nil {
		return fail(err)
	}
	output, err := planned.materialize(ctx)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkResourceValue(output, outputLimits, "mapping_output"); err != nil {
		return fail(err)
	}
	if err := mapping.output.validate(output, "/output", outputLimits); err != nil {
		return fail(err)
	}
	if packageState != nil {
		if err := compiled.finishPackageExecution(ctx, direction, output, outputLimits); err != nil {
			return fail(err)
		}
	}
	if err := options.packageTransaction.stage(state, true); err != nil {
		return fail(err)
	}
	return output, nil
}

func definitionIssues(identity Identity, path string, err error) []ConversionIssue {
	return []ConversionIssue{{Code: InvalidDefinition, Severity: SeverityError, Protocol: identity, Stage: "compile", Path: mappingErrorPath(path, err), Reason: err.Error(), Suggestion: "Correct the definition at this location before verification."}}
}
