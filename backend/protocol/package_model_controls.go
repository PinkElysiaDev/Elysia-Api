package protocol

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// ProtocolControlStep reads the current request, reports any disposition, then
// applies its field updates together. Stop ends this projection, not its checks.
type ProtocolControlStep struct {
	When       *Expression                `json:"when,omitempty"`
	Set        map[string]Expression      `json:"set,omitempty"`
	Diagnostic *ProtocolControlDiagnostic `json:"diagnostic,omitempty"`
	Stop       bool                       `json:"stop,omitempty"`
}

// ProtocolControlDiagnostic declares the observable consequence of a control
// change. Dynamic messages read the same pre-update input as the step's values.
type ProtocolControlDiagnostic struct {
	Kind       string     `json:"kind"`
	Path       string     `json:"path"`
	Reason     Expression `json:"reason"`
	Code       IssueCode  `json:"code,omitempty"`
	Direction  Direction  `json:"direction,omitempty"`
	Stage      string     `json:"stage,omitempty"`
	Suggestion string     `json:"suggestion,omitempty"`
}

type compiledControlStep struct {
	when       *compiledOutputPlan
	updates    []compiledControlUpdate
	diagnostic *compiledControlDiagnostic
	shouldStop bool
}

type compiledControlUpdate struct {
	program *compiledOutputPlan
	set     func(*Request, JSONOutput, *valueReader) error
}

type compiledControlDiagnostic struct {
	declaration ProtocolControlDiagnostic
	reason      *compiledOutputPlan
}

type modelControlOptions struct {
	CoreVersion int                   `json:"coreVersion"`
	Target      Identity              `json:"target"`
	Policy      ProtocolModelPolicy   `json:"policy"`
	Expressions map[string]Expression `json:"expressions,omitempty"`
	plan        *compiledModelPolicy
}

type modelControlExecution struct {
	plan    *compiledModelPolicy
	context Value
	rule    ConversionRule
	limits  Limits
}

func compileControlSteps(steps []ProtocolControlStep, compiler *expressionCompiler, path string, isCheck bool) ([]compiledControlStep, error) {
	if len(steps) > compiler.limits.Mutations {
		return nil, fmt.Errorf("%s: control step count exceeds mutation limit", path)
	}
	result := make([]compiledControlStep, 0, len(steps))
	scope := expressionScope{coreInput: reflect.TypeFor[Request]()}
	for index, declaration := range steps {
		at := fmt.Sprintf("%s/%d", path, index)
		if isCheck && len(declaration.Set) != 0 || len(declaration.Set) == 0 && declaration.Diagnostic == nil && !declaration.Stop {
			return nil, fmt.Errorf("%s: checks cannot change fields and every step must have an effect", at)
		}
		step := compiledControlStep{shouldStop: declaration.Stop}
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
			setter, kind, err := compileControlSetter(key)
			if err != nil {
				return nil, fmt.Errorf("%s/set/%s: %w", at, escapePointer(key), err)
			}
			program, err := compiler.compilePlan(declaration.Set[key], at+"/set/"+escapePointer(key), scope, 1)
			if err != nil {
				return nil, err
			}
			if !isAssignable(program.expression.result, kind) && program.expression.result != NullType {
				return nil, fmt.Errorf("%s/set/%s: update has the wrong core field type", at, escapePointer(key))
			}
			step.updates = append(step.updates, compiledControlUpdate{program: program, set: setter})
		}
		if diagnostic := declaration.Diagnostic; diagnostic != nil {
			if !slices.Contains([]string{"normalize", "degrade", "reject", "invalid"}, diagnostic.Kind) {
				return nil, fmt.Errorf("%s/diagnostic/kind: unsupported control diagnostic", at)
			}
			if _, err := parsePointer(diagnostic.Path); err != nil {
				return nil, fmt.Errorf("%s/diagnostic/path: %w", at, err)
			}
			if diagnostic.Kind == "invalid" {
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
		}
		result = append(result, step)
	}
	return result, nil
}

func compileControlSetter(path string) (func(*Request, JSONOutput, *valueReader) error, JSONType, error) {
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
			return writeCoreBool(output, &request.OutputLimitDefault, reader)
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
	return nil, "", fmt.Errorf("model controls can update reasoning, one parameter or outputLimitDefault")
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
	capability, err := options.plan.resolveReasoning(ctx, route.Model, route.Reasoning, conversion.limits)
	if err != nil {
		return nil, err
	}
	contextValue, err := EncodeValue(struct {
		Model               string                `json:"model"`
		Capability          ReasoningCapabilities `json:"capability"`
		IsSameWire          bool                  `json:"isSameWire"`
		IsAgent             bool                  `json:"isAgent"`
		HasTools            bool                  `json:"hasTools"`
		InterleavedThinking bool                  `json:"interleavedThinking"`
	}{route.Model, capability, route.Source.Family == route.Target.Family && route.Source.WireVersion == route.Target.WireVersion, request.Source == AgentIdentity(), len(request.Tools) != 0, route.InterleavedThinking})
	if err != nil {
		return nil, err
	}
	execution := &modelControlExecution{plan: options.plan, context: contextValue, rule: rule, limits: conversion.phaseLimits(ctx, ConversionRequest)}
	if err := execution.apply(ctx, conversion, request, route, sink, options.plan.steps); err != nil {
		return nil, err
	}
	return execution, nil
}

func (execution *modelControlExecution) apply(ctx context.Context, conversion *CompiledConversion, request *Request, route ConversionContext, sink *DiagnosticSink, steps []compiledControlStep) error {
	read := func(pointer []string) (JSONOutput, error) { return readCoreRequest(*request, pointer) }
	reader := &valueReader{}
	state := evaluation{ctx: ctx, coreRequest: request, inputReader: read, rootReader: read, context: execution.context, valueReader: reader, budget: &evaluationBudget{limits: execution.limits, stepLimit: execution.limits.Nodes}}
	for _, step := range steps {
		if step.when != nil {
			condition, err := step.when.evaluate(state)
			if err != nil {
				return err
			}
			applies, err := readBoolean(condition)
			if err != nil {
				return err
			}
			if !applies {
				continue
			}
		}
		if diagnostic := step.diagnostic; diagnostic != nil {
			message, err := diagnostic.reason.evaluate(state)
			if err != nil {
				return err
			}
			reason, err := readString(message)
			if err != nil {
				return err
			}
			declaration, rule := diagnostic.declaration, execution.rule
			switch declaration.Kind {
			case "normalize":
				conversion.normalized(rule, route, declaration.Path, reason, sink)
			case "degrade", "reject":
				if declaration.Kind == "reject" {
					rule.Action = "reject"
				}
				if err := conversion.issue(rule, ConversionRequest, route, declaration.Path, reason, sink, true); err != nil {
					return candidateFailure(err)
				}
			case "invalid":
				return IssuesError([]ConversionIssue{{Code: declaration.Code, Severity: SeverityError, Direction: declaration.Direction, Stage: declaration.Stage, Path: declaration.Path, Reason: reason, Suggestion: declaration.Suggestion}})
			}
		}
		values := make([]JSONOutput, len(step.updates))
		for index, update := range step.updates {
			value, err := update.program.build(state)
			if err != nil {
				return err
			}
			values[index] = value.output
		}
		for index, update := range step.updates {
			if err := update.set(request, values[index], reader); err != nil {
				return err
			}
		}
		if step.shouldStop {
			return nil
		}
	}
	return nil
}
