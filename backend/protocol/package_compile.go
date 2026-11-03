package protocol

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type compiledPackagePlan struct {
	continuation         *compiledContinuation
	coreVersion          int
	dslVersion           int
	effects              map[Direction][]compiledSemanticEffect
	requestProjection    Value
	hasCacheProjection   bool
	hasFieldProjection   bool
	modelContracts       map[string]compiledWireContract
	modelClientContracts map[string]compiledWireContract
	tokenCounts          map[string]*compiledTokenCounting
	taskContracts        map[string]map[string]packageTaskContracts
	finalContracts       map[Direction]compiledWireContract
	modelPolicy          *compiledModelPolicy
	conversionDefaults   []ConversionRule
	fieldObservations    map[string]compiledFieldObservation
}

type compiledSemanticEffect struct {
	id, kind, path, target, reason string
	corePointer, wirePointer       []string
	when                           *compiledOutputPlan
	layout                         *ProtocolAggregateLayout
	aggregateLayout                *compiledAggregateLayout
	valueMapping                   *ProtocolValueMapping
	valueUnmatched                 []string
	scope                          string
	severity                       Severity
	diagnostic                     *RejectDiagnostic
	defaultValue                   Value
	clearField                     func(*Request)
	conditionReferences            map[string]Expression
}

// DecodeProtocolPackage parses and validates a package without activating it.
// Validation and executable compilation share the same contract compiler.
func DecodeProtocolPackage(raw []byte, limits Limits) (ProtocolPackage, error) {
	document, err := decodeProtocolPackage(raw, limits)
	if err != nil {
		return document, err
	}
	return document, document.Validate(limits)
}

// Validate checks package contracts and programs without granting execution or
// fidelity evidence. Some declared effects require a later runtime revision.
func (document ProtocolPackage) Validate(limits Limits) error {
	_, err := document.compileContract(limits)
	return err
}

// CompilePackage compiles a spec/mapping package without installed codec modules.
// Unsupported semantic effects are rejected instead of silently ignored.
func (compiler *Compiler) CompilePackage(raw []byte) (*Compiled, []ConversionIssue) {
	document, err := decodeProtocolPackage(raw, compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/", err)
	}
	return compiler.compilePackage(document)
}

func (compiler *Compiler) compilePackage(document ProtocolPackage) (*Compiled, []ConversionIssue) {
	compiled, err := document.compileContract(compiler.limits)
	if err != nil {
		return nil, definitionIssues(document.Spec.Identity(), "/", err)
	}
	for _, operation := range compiled.operations {
		if operation.Transport == WebSocket && !compiler.features[featureWebSocketTransport] {
			return nil, definitionIssues(compiled.identity, "/spec/operations", fmt.Errorf("WebSocket transport is not installed"))
		}
	}
	if issues := compiler.checkPackagePrograms(document, compiled, "/mapping/directions"); len(issues) != 0 {
		return nil, issues
	}
	for _, name := range sortedKeys(compiled.packagePlan.tokenCounts) {
		count := compiled.packagePlan.tokenCounts[name]
		countDocument := document
		countDocument.Mapping.Directions = document.Mapping.Operations[name].CountTokens.Request
		if issues := compiler.checkPackagePrograms(countDocument, count.request, "/mapping/operations/"+escapePointer(name)+"/countTokens/request"); len(issues) != 0 {
			return nil, issues
		}
	}
	return compiled, nil
}

func (compiler *Compiler) checkPackagePrograms(document ProtocolPackage, compiled *Compiled, pathPrefix string) []ConversionIssue {
	for _, direction := range sortedKeys(compiled.packagePlan.effects) {
		if isEventDirection(direction) && compiled.wireContracts[direction].packageStream == nil && !compiled.hasPackageSession() {
			return definitionIssues(compiled.identity, pathPrefix+"/"+string(direction), fmt.Errorf("event programs require an independent wire stream contract"))
		}
		mapping := compiled.mappings[direction]
		if document.Mapping.Native.Preserve && (direction == EncodeEvent || direction == EncodeUpstreamEvent) && mapping.outputPlan != nil && mapping.outputPlan.hasStreamWrites && mapping.streamObserver == nil {
			return definitionIssues(compiled.identity, pathPrefix+"/"+string(direction)+"/observe", fmt.Errorf("native replay of stateful event programs requires an explicit observer"))
		}
		if compiled.mappings[direction].outputPlan == nil {
			return definitionIssues(compiled.identity, pathPrefix+"/"+string(direction)+"/program", fmt.Errorf("program requires a compiled output plan"))
		}
		for index, effect := range compiled.packagePlan.effects[direction] {
			if effect.kind != "emit" && effect.kind != "reject" && effect.kind != "aggregate" && !isRequestProjectionScope(effect.scope) {
				path := fmt.Sprintf("%s/%s/effects/%d/kind", pathPrefix, direction, index)
				return definitionIssues(compiled.identity, path, fmt.Errorf("semantic effect %q is not executable in this runtime", effect.kind))
			}
			if effect.kind == "aggregate" {
				path := fmt.Sprintf("%s/%s/effects/%d", pathPrefix, direction, index)
				if effect.valueMapping != nil {
					if !provesHistoryValueMapping(compiled.mappings[direction].outputPlan.expression, direction, effect, compiler.limits.Nodes) {
						return definitionIssues(compiled.identity, path, fmt.Errorf("value mapping requires a provable whole-value partition or reconstruction"))
					}
					continue
				}
				if !provesPackageAggregate(compiled.mappings[direction].outputPlan.expression, direction, effect, compiler.limits.Nodes) {
					return definitionIssues(compiled.identity, path, fmt.Errorf("aggregate requires a provable object partition or reconstruction"))
				}
			}
			if effect.kind == "emit" {
				input, output := effect.corePointer, effect.wirePointer
				if isPackageDecodeDirection(direction) {
					input, output = output, input
				}
				program := compiled.mappings[direction].outputPlan.expression
				hasProof := provesPackageEmission(program, output, input)
				if !hasProof && !isPackageDecodeDirection(direction) {
					if effect.when == nil {
						schema := document.Spec.resolvedDocumentSchema(document.Mapping.Directions[direction].Document)
						hasProof = provesDeepMergeEmission(program, output, input, schema)
					} else {
						hasProof = provesGuardedDeepMergeEmission(program, output, input, effect.when.expression, compiler.limits.Nodes)
					}
				}
				if !hasProof {
					path := fmt.Sprintf("%s/%s/effects/%d", pathPrefix, direction, index)
					return definitionIssues(compiled.identity, path, fmt.Errorf("emit effect %q requires a provable read of its declared input field", effect.id))
				}
			}
		}
	}
	return nil
}

// Package returns an independent spec/mapping document. Legacy definitions return false.
func (compiled *Compiled) Package() (ProtocolPackage, bool) {
	var document ProtocolPackage
	if compiled.packagePlan == nil {
		return document, false
	}
	_ = compiled.definition.Decode(&document)
	return document, true
}

func (compiled *Compiled) bindPackageIdentity(document ProtocolPackage) error {
	value, err := EncodeValue(document)
	if err != nil {
		return err
	}
	compiled.definition = value
	identity, err := EncodeValue(struct {
		CompilerVersion string          `json:"compilerVersion"`
		Package         ProtocolPackage `json:"package"`
	}{CompilerVersion, document})
	if err != nil {
		return err
	}
	compiled.hash = hashValue(identity)
	samples, err := EncodeValue(struct {
		CoreVersion       int                   `json:"coreVersion"`
		DSLVersion        int                   `json:"dslVersion"`
		Samples           []Sample              `json:"samples"`
		ModelSamples      []ModelSample         `json:"modelSamples,omitempty"`
		TokenCountSamples []Sample              `json:"tokenCountSamples,omitempty"`
		TaskSamples       []TaskSample          `json:"taskSamples,omitempty"`
		Agent             *ProtocolAgentMapping `json:"agent,omitempty"`
		SessionSamples    []SessionSample       `json:"sessionSamples,omitempty"`
	}{document.Mapping.CoreVersion, document.Mapping.DSLVersion, document.Mapping.Samples, document.Mapping.ModelSamples, document.Mapping.TokenCountSamples, document.Mapping.TaskSamples, document.Mapping.Agent, document.Mapping.SessionSamples})
	if err != nil {
		return err
	}
	compiled.samplesHash = hashValue(samples)
	return nil
}

func compileProtocolProgram(path string, mapping ProtocolDirectionMapping, direction Direction, wire, finalWire WireContractSpec, documentHashes map[string]string, outputNamespaces map[string]int, compiler *expressionCompiler, coreVersion, dslVersion int) (compiledMapping, []compiledSemanticEffect, error) {
	if err := validateNativeReplayMode(mapping, direction, path); err != nil {
		return compiledMapping{}, nil, err
	}
	isDecode := isPackageDecodeDirection(direction)
	if mapping.IsEventBatch && !isEventDecoder(direction) {
		return compiledMapping{}, nil, fmt.Errorf("%s/eventBatch: requires an event decoder", path)
	}
	if mapping.CanDeferEvents && (!mapping.IsEventBatch || len(mapping.State)+len(mapping.Indexes) == 0) {
		return compiledMapping{}, nil, fmt.Errorf("%s/deferEvents: requires an event batch with declared state", path)
	}
	if mapping.IsFrameBatch && direction != EncodeEvent && direction != EncodeUpstreamEvent {
		return compiledMapping{}, nil, fmt.Errorf("%s/frameBatch: requires an event encoder", path)
	}
	usesStreamPrimitive := false
	scope := expressionScope{canReadRuntime: true, canReadOutputStream: direction == EncodeEvent, outputNamespaces: outputNamespaces, canUseStreamPrimitives: isEventDirection(direction), streamPrimitiveUsed: &usesStreamPrimitive, trackerChannels: map[string]bool{}}
	if isDecode {
		scope.input = wire.Schema
	} else {
		scope.coreInput = packageCoreInputType(direction)
	}
	references := map[string]Expression{}
	compiler.mappingReferences = references
	defer func() { compiler.mappingReferences = nil }()
	var nativeWhen *compiledOutputPlan
	if mapping.NativeWhen != nil {
		if mapping.NativeReplay != "original_frame" {
			return compiledMapping{}, nil, fmt.Errorf("%s/nativeWhen: requires original_frame replay", path)
		}
		// Replay selection can read caller preferences and whether this encoder
		// has rendered earlier events. It cannot change state or delivery facts.
		predicate, err := compiler.compilePlan(*mapping.NativeWhen, path+"/nativeWhen", expressionScope{input: wire.Schema, root: wire.Schema, isWireContract: true, canReadClientOutput: true, canReadEncodingState: true}, 1)
		if err != nil {
			return compiledMapping{}, nil, err
		}
		if !isAssignable(predicate.expression.result, BooleanType) {
			return compiledMapping{}, nil, fmt.Errorf("%s/nativeWhen: requires a boolean frame predicate", path)
		}
		nativeWhen = predicate
	}
	var finalCheck *compiledOutputPlan
	if mapping.FinalCheck != nil {
		if direction != EncodeRequest {
			return compiledMapping{}, nil, fmt.Errorf("%s/finalCheck: requires request encoding", path)
		}
		check, err := compiler.compilePlan(*mapping.FinalCheck, path+"/finalCheck", expressionScope{input: finalWire.Schema, root: finalWire.Schema, isWireContract: true, canReadClientOutput: true}, 1)
		if err != nil {
			return compiledMapping{}, nil, err
		}
		if !isAssignable(check.expression.result, BooleanType) {
			return compiledMapping{}, nil, fmt.Errorf("%s/finalCheck: requires a boolean request predicate", path)
		}
		finalCheck = check
	}
	if direction == EncodeEvent && len(mapping.DeliveryRanges) != 0 && finalWire.Stream == nil {
		return compiledMapping{}, nil, fmt.Errorf("%s/deliveryRanges: requires a final wire stream contract", path)
	}
	deliveryRanges, err := compileDeliveryRanges(mapping.DeliveryRanges, direction, finalWire.Schema, path+"/deliveryRanges", compiler)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	if len(mapping.SemanticRanges) != 0 && (direction != EncodeEvent || !mapping.IsFrameBatch) {
		return compiledMapping{}, nil, fmt.Errorf("%s/semanticRanges: requires frame-batch event encoding", path)
	}
	semanticRanges, err := compileDeliveryRanges(mapping.SemanticRanges, direction, &semanticRangeEventSchema, path+"/semanticRanges", compiler)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	indexes, err := compilePackageIndexes(mapping.Indexes, direction, path+"/indexes", compiler.limits, &scope)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	streamState, err := compilePackageState(mapping.State, indexes, direction, path+"/state", compiler, &scope)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	if (nativeWhen != nil && nativeWhen.hasEncodingStateRead || len(semanticRanges) != 0) && streamState == nil {
		streamState = &compiledPackageState{initial: []Value{}}
	}
	var finalizer *compiledOutputPlan
	if mapping.Finalize != nil {
		if !mapping.IsFrameBatch {
			return compiledMapping{}, nil, fmt.Errorf("%s/finalize: requires a frame-batch event encoder", path)
		}
		finalizerScope := scope
		finalizerScope.canRequireDisposition = direction == EncodeEvent
		finalizerScope.canWriteStreamIndexes = true
		finalizer, err = compiler.compilePlan(*mapping.Finalize, path+"/finalize", finalizerScope, 1)
		if err != nil {
			return compiledMapping{}, nil, err
		}
		if !isAssignable(finalizer.expression.result, ArrayType) || hasCurrentEventRead(finalizer) {
			return compiledMapping{}, nil, fmt.Errorf("%s/finalize: requires a frame array without current-event reads", path)
		}
		if err := checkExpressionOutput(finalizer.expression, &ValueSchema{Type: ArrayType, Items: wire.Schema}, compiler.limits); err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/finalize: %w", path, err)
		}
		if streamState == nil {
			streamState = &compiledPackageState{initial: []Value{}}
		}
	}
	var observer *compiledOutputPlan
	if mapping.Observe != nil {
		if direction != EncodeEvent && direction != EncodeUpstreamEvent {
			return compiledMapping{}, nil, fmt.Errorf("%s/observe: requires an event encoder", path)
		}
		observerScope := scope
		observerScope.canRequireDisposition = direction == EncodeEvent
		observerScope.canWriteStreamIndexes, observerScope.canReadNativeFrame, observerScope.canReadNative = true, true, true
		observer, err = compiler.compilePlan(*mapping.Observe, path+"/observe", observerScope, 1)
		if err != nil {
			return compiledMapping{}, nil, err
		}
	}
	programScope := scope
	programScope.canRequireDisposition = direction == EncodeEvent
	programScope.canWriteStreamIndexes = true
	programScope.wireCapture = isDecode
	programScope.canSelectCarrier = direction == DecodeRequest
	programScope.canReadNative = !isDecode
	program, err := compiler.compile(mapping.Program, path+"/program", programScope, 1)
	if err != nil {
		return compiledMapping{}, nil, err
	}
	if usesStreamPrimitive && streamState == nil {
		streamState = &compiledPackageState{initial: []Value{}}
	}
	if mapping.IsEventBatch && !isAssignable(program.result, ArrayType) {
		return compiledMapping{}, nil, fmt.Errorf("%s/program: eventBatch requires an array output", path)
	}
	if mapping.IsFrameBatch && !isAssignable(program.result, ArrayType) {
		return compiledMapping{}, nil, fmt.Errorf("%s/program: frameBatch requires an array output", path)
	}
	if !isDecode {
		schema := wire.Schema
		if mapping.IsFrameBatch {
			schema = &ValueSchema{Type: ArrayType, Items: schema}
		}
		if err := checkExpressionOutput(program, schema, compiler.limits); err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/program: %w", path, err)
		}
	}
	if len(mapping.Effects) == 0 && !isEventDirection(direction) || len(mapping.Effects) > compiler.limits.Mutations {
		return compiledMapping{}, nil, fmt.Errorf("%s/effects: requires a nonempty bounded effect list", path)
	}
	seen := map[string]bool{}
	effects := make([]compiledSemanticEffect, 0, len(mapping.Effects))
	for index, effect := range mapping.Effects {
		at := fmt.Sprintf("%s/effects/%d", path, index)
		if !definitionIdentifier.MatchString(effect.ID) || seen[effect.ID] {
			return compiledMapping{}, nil, fmt.Errorf("%s/id: expected a unique effect identifier", at)
		}
		seen[effect.ID] = true
		switch effect.Kind {
		case "emit", "aggregate", "reject":
		case "default", "drop":
			if isDecode {
				return compiledMapping{}, nil, fmt.Errorf("%s/kind: decode programs cannot default or drop input semantics", at)
			}
		default:
			return compiledMapping{}, nil, fmt.Errorf("%s/kind: unknown semantic effect %q", at, effect.Kind)
		}
		if (effect.Kind == "default" || effect.Kind == "drop" || effect.Kind == "reject") && strings.TrimSpace(effect.Reason) == "" {
			return compiledMapping{}, nil, fmt.Errorf("%s/reason: semantic disposition requires a reason", at)
		}
		if err := validateRequestProjectionEffect(direction, effect); err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s: %w", at, err)
		}
		entry := compiledSemanticEffect{id: effect.ID, kind: effect.Kind, path: effect.Path, target: effect.Target, reason: effect.Reason, scope: effect.Scope, severity: effect.Severity}
		if effect.Diagnostic != nil {
			if effect.Diagnostic.Direction != "" || effect.Diagnostic.Path != "" {
				return compiledMapping{}, nil, fmt.Errorf("%s/diagnostic: direction and path overrides belong to wire assertions", at)
			}
			if err := validateDiagnosticClassification(effect.Diagnostic.Code, effect.Diagnostic.Stage, at+"/diagnostic"); err != nil {
				return compiledMapping{}, nil, err
			}
			diagnostic := *effect.Diagnostic
			entry.diagnostic = &diagnostic
		}
		entry.corePointer, err = parsePointer(effect.Path)
		if err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/path: %w", at, err)
		}
		entry.wirePointer, err = parsePointer(effect.Target)
		if err != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s/target: %w", at, err)
		}
		if effect.Scope == requestFieldScope {
			entry.defaultValue = effect.Default
		}
		if effect.Kind == "aggregate" {
			if effect.ValueMapping != nil {
				if effect.Layout != nil {
					return compiledMapping{}, nil, fmt.Errorf("%s: aggregate cannot combine object and value mappings", at)
				}
				if err := validateFiniteValueMapping(direction, effect.Path, effect.ValueMapping, compiler.limits); err != nil {
					return compiledMapping{}, nil, fmt.Errorf("%s/valueMapping: %w", at, err)
				}
				entry.valueMapping = effect.ValueMapping
				entry.valueUnmatched, _ = parsePointer(effect.ValueMapping.Unmatched)
			} else {
				entry.aggregateLayout, err = compilePackageAggregateLayout(direction, entry.corePointer, effect.Layout)
				if err != nil {
					return compiledMapping{}, nil, fmt.Errorf("%s/layout: %w", at, err)
				}
			}
			entry.layout = effect.Layout
		} else if effect.Layout != nil || effect.ValueMapping != nil {
			return compiledMapping{}, nil, fmt.Errorf("%s: only aggregate effects have an object or value mapping", at)
		}
		if effect.When != nil {
			conditionScope := scope
			if isRequestProjectionScope(effect.Scope) {
				conditionScope = expressionScope{}
				entry.conditionReferences = map[string]Expression{}
				compiler.mappingReferences = entry.conditionReferences
			}
			condition, err := compiler.compile(*effect.When, at+"/when", conditionScope, 1)
			compiler.mappingReferences = references
			for name, expression := range entry.conditionReferences {
				references[name] = expression
			}
			if err != nil {
				return compiledMapping{}, nil, err
			}
			if !isAssignable(condition.result, BooleanType) {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: expected a boolean expression", at)
			}
			var hasPlan bool
			entry.when, hasPlan = compileOutputPlan(condition)
			if !hasPlan {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: condition requires a compiled output plan", at)
			}
			if isRequestProjectionScope(effect.Scope) && !localProjectionCondition(entry.when) {
				return compiledMapping{}, nil, fmt.Errorf("%s/when: projection conditions may read only their local input or root", at)
			}
		}
		effects = append(effects, entry)
	}
	identity, err := EncodeValue(struct {
		CompilerVersion string                   `json:"compilerVersion"`
		CoreVersion     int                      `json:"coreVersion"`
		DSLVersion      int                      `json:"dslVersion"`
		Direction       Direction                `json:"direction"`
		WireHash        string                   `json:"wireHash"`
		FinalWireHash   string                   `json:"finalWireHash,omitempty"`
		Mapping         ProtocolDirectionMapping `json:"mapping"`
		References      map[string]Expression    `json:"references"`
	}{CompilerVersion, coreVersion, dslVersion, direction, documentHashes[mapping.Document], documentHashes[mapping.FinalDocument], mapping, references})
	if err != nil {
		return compiledMapping{}, nil, err
	}
	plan, hasPlan := compileOutputPlan(program)
	if !hasPlan {
		return compiledMapping{}, nil, fmt.Errorf("%s/program: expression has no compiled execution plan", path)
	}
	return compiledMapping{
		preservesDynamicNative: mapping.NativeReplay == "original_frame",
		nativeWhen:             nativeWhen,
		finalCheck:             finalCheck,
		deliveryRanges:         deliveryRanges,
		semanticRanges:         semanticRanges,
		outputPlan:             plan, definitionHash: hashValue(identity), streamState: streamState, streamFinalizer: finalizer, streamObserver: observer,
		isEventBatch:      mapping.IsEventBatch,
		canDeferEvents:    mapping.CanDeferEvents,
		frameBatch:        mapping.IsFrameBatch,
		hasCoreOutputPlan: isPackageDecodeDirection(direction),
		hasCoreInputPlan:  !isPackageDecodeDirection(direction),
	}, effects, nil
}

func validateNativeReplayMode(mapping ProtocolDirectionMapping, direction Direction, path string) error {
	if mapping.NativeReplay == "" {
		return nil
	}
	if mapping.NativeReplay != "original_frame" || direction != EncodeEvent || !mapping.IsFrameBatch || mapping.Observe == nil {
		return fmt.Errorf("%s/nativeReplay: original_frame requires a frame-batch event encoder and a native observer", path)
	}
	return nil
}

func isPackageDecodeDirection(direction Direction) bool {
	return direction == DecodeRequest || direction == DecodeResponse || isEventDecoder(direction)
}

// An emit is a field copy, not an author-supplied claim that arbitrary code is
// lossless. Resolve constructors once so execution never compares large values.
func provesPackageEmission(program *compiledExpression, output, input []string) bool {
	if program == nil {
		return false
	}
	switch program.op {
	case "reject":
		// A branch that cannot return output cannot violate a successful emit.
		return true
	case "let":
		// Binding a local value does not replace the input observed by body
		// reads. The proof still refuses reads from the binding itself.
		return provesPackageEmission(program.body, output, input)
	case "read":
		return program.from == "input" && len(input) == len(program.pointer)+len(output) &&
			slices.Equal(input[:len(program.pointer)], program.pointer) && slices.Equal(input[len(program.pointer):], output)
	case "if":
		return provesPackageEmission(program.then, output, input) && provesPackageEmission(program.otherwise, output, input)
	case "choose":
		if !provesPackageEmission(program.otherwise, output, input) {
			return false
		}
		for _, branch := range program.cases {
			if !provesPackageEmission(branch, output, input) {
				return false
			}
		}
		return true
	case "merge":
		// Emit preflight establishes that the copied input exists. In a last
		// merge, the final operand's whole member cannot be overwritten later.
		return len(output) > 0 && program.policy == "last" && len(program.items) > 0 && provesPackageEmission(program.items[len(program.items)-1], output, input)
	}
	if len(output) == 0 {
		return false
	}
	switch program.op {
	case "object":
		return provesPackageEmission(program.fields[output[0]], output[1:], input)
	case "array":
		index, err := strconv.Atoi(output[0])
		if err != nil || index < 0 || index >= len(program.items) || strconv.Itoa(index) != output[0] {
			return false
		}
		for _, item := range program.items[:index] {
			if !alwaysPresentOutput(item) {
				return false
			}
		}
		return provesPackageEmission(program.items[index], output[1:], input)
	}
	return false
}

func alwaysPresentOutput(program *compiledExpression) bool {
	if program == nil {
		return false
	}
	switch program.op {
	case "object", "array", "literal", "reject":
		return true
	case "read":
		return program.isRequired
	case "if":
		return alwaysPresentOutput(program.then) && alwaysPresentOutput(program.otherwise)
	case "choose":
		if !alwaysPresentOutput(program.otherwise) {
			return false
		}
		for _, branch := range program.cases {
			if !alwaysPresentOutput(branch) {
				return false
			}
		}
		return true
	}
	return false
}
