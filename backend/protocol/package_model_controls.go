package protocol

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// ProtocolControlStep reads the current core value, reports any disposition,
// then applies its field updates together. Stop ends the local step sequence.
type ProtocolControlStep struct {
	When       *Expression                   `json:"when,omitempty"`
	Set        map[string]Expression         `json:"set,omitempty"`
	Diagnostic *ProtocolControlDiagnostic    `json:"diagnostic,omitempty"`
	Stop       bool                          `json:"stop,omitempty"`
	Each       *ProtocolControlEach          `json:"each,omitempty"`
	Auxiliary  *ProtocolAuxiliaryDisposition `json:"auxiliary,omitempty"`
	Omit       bool                          `json:"omit,omitempty"`
}

// ProtocolControlEach selects typed tools, tool results, nodes, metadata, usage
// or error fields. Input is local; root remains the complete core document.
type ProtocolControlEach struct {
	Select      string                `json:"select" jsonschema:"enum=node,enum=tool,enum=tool_result,enum=metadata,enum=usage,enum=error"`
	PathBinding string                `json:"pathBinding,omitempty"`
	Steps       []ProtocolControlStep `json:"steps"`
}

// ProtocolControlDiagnostic declares the observable consequence of a control
// change. Dynamic messages read the same pre-update input as the step's values.
type ProtocolControlDiagnostic struct {
	Kind        string      `json:"kind"`
	Attribution string      `json:"attribution,omitempty"`
	Path        string      `json:"path,omitempty"`
	Source      *Expression `json:"source,omitempty"`
	Reason      Expression  `json:"reason"`
	Code        IssueCode   `json:"code,omitempty"`
	Direction   Direction   `json:"direction,omitempty"`
	Stage       string      `json:"stage,omitempty"`
	Suggestion  string      `json:"suggestion,omitempty"`
}

type compiledControlStep struct {
	when       *compiledOutputPlan
	updates    []compiledControlUpdate
	diagnostic *compiledControlDiagnostic
	shouldStop bool
	shouldOmit bool
	each       *compiledControlEach
	auxiliary  *ProtocolAuxiliaryDisposition
}

type compiledControlUpdate struct {
	program          *compiledOutputPlan
	set              func(any, JSONOutput, *valueReader) error
	changesReasoning bool
}

type compiledControlDiagnostic struct {
	declaration ProtocolControlDiagnostic
	reason      *compiledOutputPlan
	source      *compiledOutputPlan
}

type modelControlOptions struct {
	CoreVersion int                        `json:"coreVersion"`
	Target      Identity                   `json:"target"`
	Policy      ProtocolModelPolicy        `json:"policy"`
	Expressions map[string]Expression      `json:"expressions,omitempty"`
	Source      *modelNormalizationOptions `json:"source,omitempty"`
	plan        *compiledModelPolicy
}

type modelNormalizationOptions struct {
	Source       Identity              `json:"source"`
	Steps        []ProtocolControlStep `json:"steps"`
	Checks       []ProtocolControlStep `json:"checks,omitempty"`
	Expressions  map[string]Expression `json:"expressions,omitempty"`
	steps        []compiledControlStep
	checks       []compiledControlStep
	bindingSlots int
}

func (source *modelNormalizationOptions) compile(compiler *expressionCompiler) error {
	var err error
	source.steps, err = compileControlSteps(source.Steps, compiler, "/mapping/modelPolicy/normalize", false)
	if err != nil {
		return err
	}
	source.checks, err = compileControlSteps(source.Checks, compiler, "/mapping/modelPolicy/normalizeChecks", true)
	source.bindingSlots = controlBindingSlots(source.steps, source.checks)
	return err
}

func controlBindingSlots(sequences ...[]compiledControlStep) int {
	count := 0
	for _, sequence := range sequences {
		for _, step := range sequence {
			if step.each != nil {
				count = max(count, step.each.pathSlot+1, controlBindingSlots(step.each.steps))
			}
			if step.when != nil {
				count = max(count, step.when.bindingSlots)
			}
			if step.diagnostic != nil {
				count = max(count, step.diagnostic.reason.bindingSlots)
				if step.diagnostic.source != nil {
					count = max(count, step.diagnostic.source.bindingSlots)
				}
			}
			for _, update := range step.updates {
				count = max(count, update.program.bindingSlots)
			}
		}
	}
	return count
}

type modelControlExecution struct {
	plan               *compiledModelPolicy
	context            Value
	contextReader      *planObjectReader
	bindings           []JSONOutput
	rule               ConversionRule
	limits             Limits
	changedReasoning   bool
	stoppedProjection  bool
	validatedReasoning reasoningValidationStamp
}

// Only core validity is reused here. Model, binding and operation constraints
// still run after authored rules, against this execution's fixed context.
type reasoningValidationStamp struct {
	fields        [6]immutableSpan
	hasReasoning  bool
	isInterleaved bool
}

func stampReasoningValidation(reasoning *ReasoningIntent) reasoningValidationStamp {
	if reasoning == nil {
		return reasoningValidationStamp{}
	}
	scope := ""
	if controlPresent(reasoning.Effort) {
		scope = reasoning.EffortScope
	}
	return reasoningValidationStamp{hasReasoning: true, isInterleaved: reasoning.Interleaved, fields: [6]immutableSpan{
		spanIdentity(reasoning.Mode), spanIdentity(reasoning.Effort.raw), spanIdentity(scope),
		spanIdentity(reasoning.Budget.raw), spanIdentity(reasoning.Summary.raw), spanIdentity(reasoning.HistoryRetention.raw),
	}}
}

func compileControlSteps(steps []ProtocolControlStep, compiler *expressionCompiler, path string, isCheck bool) ([]compiledControlStep, error) {
	scope := controlCompileScope{expression: expressionScope{coreInput: reflect.TypeFor[Request]()}, whole: reflect.TypeFor[Request]()}
	return compileScopedControlSteps(steps, compiler, path, isCheck, scope, new(int))
}

func compileConversionControlSteps(steps []ProtocolControlStep, compiler *expressionCompiler, path string, root reflect.Type, frameUsage bool) ([]compiledControlStep, error) {
	canReadOperation := root == reflect.TypeFor[Response]() || root == reflect.TypeFor[Event]()
	scope := controlCompileScope{expression: expressionScope{coreInput: root, canReadFrameUsage: frameUsage, canReadOperation: canReadOperation}, whole: root, canSelect: true, canUseAuxiliary: root == reflect.TypeFor[Event]()}
	return compileScopedControlSteps(steps, compiler, path, false, scope, new(int))
}

func compileScopedControlSteps(steps []ProtocolControlStep, compiler *expressionCompiler, path string, isCheck bool, context controlCompileScope, count *int) ([]compiledControlStep, error) {
	if len(steps) > compiler.limits.Mutations {
		return nil, fmt.Errorf("%s: control step count exceeds mutation limit", path)
	}
	result := make([]compiledControlStep, 0, len(steps))
	scope, root := context.expression, context.expression.coreInput
	for index, declaration := range steps {
		at := fmt.Sprintf("%s/%d", path, index)
		*count = *count + 1
		if *count > compiler.limits.Mutations {
			return nil, fmt.Errorf("%s: complete control program exceeds mutation limit", path)
		}
		if isCheck && (len(declaration.Set) != 0 || declaration.Each != nil || declaration.Auxiliary != nil || declaration.Omit) || len(declaration.Set) == 0 && declaration.Diagnostic == nil && !declaration.Stop && declaration.Each == nil && declaration.Auxiliary == nil && !declaration.Omit {
			return nil, fmt.Errorf("%s: checks cannot change fields and every step must have an effect", at)
		}
		step := compiledControlStep{shouldStop: declaration.Stop, shouldOmit: declaration.Omit}
		if err := validateControlStepForm(declaration, context, at); err != nil {
			return nil, err
		}
		if declaration.Each != nil {
			var err error
			step.each, err = compileControlEach(*declaration.Each, compiler, at+"/each", context, count)
			if err != nil {
				return nil, err
			}
		}
		if declaration.Auxiliary != nil {
			copy := *declaration.Auxiliary
			step.auxiliary = &copy
		}
		if declaration.When != nil {
			condition, err := compiler.compilePlan(*declaration.When, at+"/when", scope, 1)
			if err != nil {
				return nil, err
			}
			if !isAssignable(condition.expression.result, BooleanType) {
				return nil, fmt.Errorf("%s/when: condition must produce a boolean", at)
			}
			step.when = condition
		}
		for _, key := range sortedKeys(declaration.Set) {
			if context.isToolResult && key == "/children" && declaration.Set[key].Op != "omit" {
				return nil, fmt.Errorf("%s/set/%s: tool result content may only be explicitly consumed", at, escapePointer(key))
			}
			setter, kind, err := context.compileSetter(root, key)
			if err != nil {
				return nil, fmt.Errorf("%s/set/%s: %w", at, escapePointer(key), err)
			}
			program, err := compiler.compilePlan(declaration.Set[key], at+"/set/"+escapePointer(key), scope, 1)
			if err != nil {
				return nil, err
			}
			if !isAssignable(program.expression.result, kind) && !(kind == ObjectType && program.expression.result == NullType) {
				return nil, fmt.Errorf("%s/set/%s: update has the wrong core field type", at, escapePointer(key))
			}
			step.updates = append(step.updates, compiledControlUpdate{program: program, set: setter, changesReasoning: key == "/reasoning"})
		}
		if diagnostic := declaration.Diagnostic; diagnostic != nil {
			if diagnostic.Attribution != "" && diagnostic.Attribution != "source" && diagnostic.Attribution != "target" {
				return nil, fmt.Errorf("%s/diagnostic/attribution: expected source or target", at)
			}
			if !slices.Contains([]string{"normalize", "degrade", "reject", "invalid"}, diagnostic.Kind) {
				return nil, fmt.Errorf("%s/diagnostic/kind: unsupported control diagnostic", at)
			}
			if diagnostic.Source != nil && diagnostic.Path != "" {
				return nil, fmt.Errorf("%s/diagnostic: source and path are mutually exclusive", at)
			}
			if _, err := parsePointer(diagnostic.Path); err != nil {
				return nil, fmt.Errorf("%s/diagnostic/path: %w", at, err)
			}
			if diagnostic.Kind == "invalid" {
				if diagnostic.Attribution != "" {
					return nil, fmt.Errorf("%s/diagnostic: invalid results use their explicit direction", at)
				}
				if !slices.Contains(DiagnosticCatalog(), diagnostic.Code) || diagnostic.Stage == "" || diagnostic.Direction != "" && !slices.Contains(DirectionCatalog(), diagnostic.Direction) {
					return nil, fmt.Errorf("%s/diagnostic: invalid result requires a declared code and stage", at)
				}
			} else if diagnostic.Code != "" || diagnostic.Direction != "" || diagnostic.Stage != "" || diagnostic.Suggestion != "" {
				return nil, fmt.Errorf("%s/diagnostic: only invalid results override error classification", at)
			}
			reason, err := compiler.compilePlan(diagnostic.Reason, at+"/diagnostic/reason", scope, 1)
			if err != nil {
				return nil, err
			}
			if !isAssignable(reason.expression.result, StringType) {
				return nil, fmt.Errorf("%s/diagnostic/reason: message must be a string", at)
			}
			step.diagnostic = &compiledControlDiagnostic{declaration: *diagnostic, reason: reason}
			if diagnostic.Source != nil {
				step.diagnostic.source, err = compiler.compilePlan(*diagnostic.Source, at+"/diagnostic/source", scope, 1)
				if err != nil {
					return nil, err
				}
				if !isAssignable(step.diagnostic.source.expression.result, StringType) {
					return nil, fmt.Errorf("%s/diagnostic/source: location must be a string pointer", at)
				}
			}
		}
		result = append(result, step)
	}
	return result, nil
}

func compileControlSetter(path string) (func(*Request, JSONOutput, *valueReader) error, JSONType, error) {
	if path == "/clientOutput/responsesIncludeResolved" {
		return func(request *Request, output JSONOutput, _ *valueReader) error {
			if request.ClientOutput == nil {
				return &declaredRejectionError{diagnostic: RejectDiagnostic{Code: InvalidInput, Path: "/clientOutput"}, reason: "include disposition requires an existing source intent"}
			}
			value, err := readPlannedBoolean(output)
			if err == nil {
				request.ClientOutput.ResponsesIncludeResolved = value
			}
			return err
		}, BooleanType, nil
	}
	if path == "/clientOutput/responsesStorage/effective" || path == "/clientOutput/responsesStorage/effectiveUnknown" {
		return compileStorageControlSetter(path), BooleanType, nil
	}
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, "", err
	}
	if len(pointer) == 2 && pointer[0] == "parameters" {
		name := pointer[1]
		return func(request *Request, output JSONOutput, reader *valueReader) error {
			if output.IsZero() {
				delete(request.Parameters, name)
				return nil
			}
			var value Value
			if err := assignCoreValue(output, &value, reader); err != nil {
				return err
			}
			if request.Parameters == nil {
				request.Parameters = Object{}
			}
			request.Parameters[name] = value
			return nil
		}, AnyType, nil
	}
	if path == "/outputLimitDefault" {
		return func(request *Request, output JSONOutput, reader *valueReader) error {
			value, err := readPlannedBoolean(output)
			if err == nil {
				request.OutputLimitDefault = value
			}
			return err
		}, BooleanType, nil
	}
	if path == "/reasoning" {
		return func(request *Request, output JSONOutput, reader *valueReader) error {
			request.Reasoning = nil
			if output.IsZero() {
				return nil
			}
			return writeCoreReasoningIntentPointer(output, &request.Reasoning, reader)
		}, ObjectType, nil
	}
	return nil, "", fmt.Errorf("request controls cannot write %s", path)
}

func compileStorageControlSetter(path string) func(*Request, JSONOutput, *valueReader) error {
	selectField := func(storage *ResponsesStorageIntent) *bool { return &storage.Effective }
	if path == "/clientOutput/responsesStorage/effectiveUnknown" {
		selectField = func(storage *ResponsesStorageIntent) *bool { return &storage.EffectiveUnknown }
	}
	return func(request *Request, output JSONOutput, _ *valueReader) error {
		if request.ClientOutput == nil || request.ClientOutput.ResponsesStorage == nil {
			return &declaredRejectionError{diagnostic: RejectDiagnostic{Code: InvalidInput, Path: "/clientOutput/responsesStorage"}, reason: "storage disposition requires an existing source intent"}
		}
		value, err := readPlannedBoolean(output)
		if err == nil {
			*selectField(request.ClientOutput.ResponsesStorage) = value
		}
		return err
	}
}

func compileModelControlOptions(raw Value, limits Limits) (conversionOption, error) {
	var options modelControlOptions
	if err := decodeContract(raw.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || len(options.Policy.Steps) == 0 {
		return conversionOption{}, fmt.Errorf("model controls require a core version, target and nonempty steps")
	}
	compiler := &expressionCompiler{limits: limits, references: options.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
	plan, err := compileModelPolicy(&options.Policy, compiler)
	if err != nil {
		return conversionOption{}, err
	}
	options.plan = plan
	if options.Source != nil {
		if options.Source.Source.DefinitionID == "" {
			return conversionOption{}, fmt.Errorf("model normalization requires its source identity")
		}
		sourceCompiler := &expressionCompiler{limits: limits, references: options.Source.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
		if err := options.Source.compile(sourceCompiler); err != nil {
			return conversionOption{}, err
		}
	}
	return conversionOption{raw: raw, value: options}, nil
}

func (conversion *CompiledConversion) applyModelControls(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (*modelControlExecution, error) {
	options, err := readConversionOption[modelControlOptions](conversion, rule)
	if err != nil {
		return nil, err
	}
	if options.Target != route.Target || request.SchemaVersion != options.CoreVersion {
		return nil, candidateFailure(streamIssue(InvalidInput, "/modelPolicy", "model controls do not match the selected target"))
	}
	bindings := options.plan.bindingSlots
	if options.Source != nil {
		bindings = max(bindings, options.Source.bindingSlots)
	}
	execution := &modelControlExecution{plan: options.plan, bindings: make([]JSONOutput, bindings), rule: rule, limits: conversion.phaseLimits(ctx, ConversionRequest)}
	if options.Source != nil {
		if options.Source.Source != route.Source {
			return nil, streamIssue(InvalidInput, "/modelPolicy/source", "model normalization does not match the selected source")
		}
		if err := execution.bindContext(request, route, nil); err != nil {
			return nil, err
		}
		if err := execution.apply(ctx, conversion, request, route, sink, options.Source.steps, false); err != nil {
			return nil, err
		}
	}
	if err := ValidateReasoningIntent(request.Reasoning); err != nil {
		return nil, err
	}
	if options.Source != nil && execution.changedReasoning {
		if err := execution.apply(ctx, conversion, request, route, sink, options.Source.checks, false); err != nil {
			return nil, err
		}
	}
	capability, err := options.plan.resolveReasoning(ctx, route.Model, route.Reasoning, conversion.limits)
	if err != nil {
		return nil, err
	}
	if err := execution.bindContext(request, route, &capability); err != nil {
		return nil, err
	}
	execution.changedReasoning = false
	if err := execution.apply(ctx, conversion, request, route, sink, options.plan.steps, true); err != nil {
		return nil, err
	}
	if execution.changedReasoning {
		if err := ValidateReasoningIntent(request.Reasoning); err != nil {
			return nil, candidateFailure(err)
		}
	}
	if !execution.stoppedProjection && request.Reasoning != nil {
		if err := execution.apply(ctx, conversion, request, route, sink, options.plan.renderChecks, false); err != nil {
			return nil, candidateFailure(err)
		}
	}
	execution.validatedReasoning = stampReasoningValidation(request.Reasoning)
	return execution, nil
}

func (execution *modelControlExecution) bindContext(request *Request, route ConversionContext, capability *ReasoningCapabilities) error {
	contextValue, err := EncodeValue(struct {
		Model               string                 `json:"model"`
		Capability          *ReasoningCapabilities `json:"capability,omitempty"`
		IsSameWire          bool                   `json:"isSameWire"`
		IsAgent             bool                   `json:"isAgent"`
		HasTools            bool                   `json:"hasTools"`
		InterleavedThinking bool                   `json:"interleavedThinking"`
	}{route.Model, capability, route.Source.Family == route.Target.Family && route.Source.WireVersion == route.Target.WireVersion, request.Source == AgentIdentity(), len(request.Tools) != 0, route.InterleavedThinking})
	if err != nil {
		return err
	}
	execution.context, execution.contextReader = contextValue, &planObjectReader{root: spanIdentity(contextValue.raw)}
	return nil
}

func (execution *modelControlExecution) validateFinalCore(request *Request) error {
	if stampReasoningValidation(request.Reasoning) == execution.validatedReasoning {
		return nil
	}
	return ValidateReasoningIntent(request.Reasoning)
}

func (execution *modelControlExecution) apply(ctx context.Context, conversion *CompiledConversion, request *Request, route ConversionContext, sink *DiagnosticSink, steps []compiledControlStep, isProjection bool) error {
	read := func(pointer []string) (JSONOutput, error) { return readCoreRequest(*request, pointer) }
	reader := &valueReader{}
	state := evaluation{ctx: ctx, coreRequest: request, inputReader: read, rootReader: read, context: execution.context, valueReader: reader, objectReader: execution.contextReader, bindings: execution.bindings, budget: &evaluationBudget{limits: execution.limits, stepLimit: execution.limits.Nodes}}
	result, err := applyControlSteps(state, conversion, request, route, execution.rule, sink, steps, nil)
	execution.changedReasoning = execution.changedReasoning || result.changedReasoning
	execution.stoppedProjection = execution.stoppedProjection || isProjection && result.shouldStop
	return err
}
