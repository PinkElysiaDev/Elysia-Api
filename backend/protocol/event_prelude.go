package protocol

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// ProtocolEventPrelude declares a target-owned opening header for a stream
// without response.started. Response may contain only id, model and attributes;
// provider content, accounting and provenance cannot be manufactured here.
type ProtocolEventPrelude struct {
	When     Expression `json:"when"`
	Response Expression `json:"response"`
	Reason   string     `json:"reason"`
}

type eventPreludeOptions struct {
	CoreVersion int                   `json:"coreVersion"`
	Target      Identity              `json:"target"`
	EncoderHash string                `json:"encoderHash"`
	Limits      Limits                `json:"limits"`
	Prelude     ProtocolEventPrelude  `json:"prelude"`
	Expressions map[string]Expression `json:"expressions,omitempty"`
	when        *compiledOutputPlan
	response    *compiledOutputPlan
}

type eventPreludeHeader struct {
	ID         Value  `json:"id,omitzero"`
	Model      Value  `json:"model,omitzero"`
	Attributes Object `json:"attributes,omitempty"`
}

func (compiled *Compiled) compileEventPrelude(declaration ProtocolConversionDefault, compiler *expressionCompiler) (ConversionRule, error) {
	if declaration.Phase != ConversionEvent || declaration.Use != "" || declaration.Recovery != nil || declaration.StreamDisposition != nil || declaration.Steps != nil || declaration.Auxiliary != nil || declaration.Control != nil || declaration.Collection || declaration.FrameUsage || declaration.Match.NodeKind != "" || declaration.Match.Path != "" || declaration.Match.Present != nil {
		return ConversionRule{}, fmt.Errorf("prelude requires a standalone event rule with route conditions only")
	}
	options := eventPreludeOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity,
		EncoderHash: compiled.mappings[EncodeEvent].definitionHash, Limits: compiler.limits, Prelude: *declaration.Prelude}
	previous := compiler.mappingReferences
	compiler.mappingReferences = map[string]Expression{}
	err := options.compile(compiler, "/prelude")
	options.Expressions = compiler.mappingReferences
	compiler.mappingReferences = previous
	if err != nil {
		return ConversionRule{}, err
	}
	value, err := EncodeValue(options)
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "event_prelude", Value: value}
	fingerprint, err := conversionRuleFingerprint(rule)
	if err != nil {
		return ConversionRule{}, err
	}
	options.Prelude.When, options.Prelude.Response, options.Expressions = Expression{}, Expression{}, nil
	rule.preparedOption = &preparedConversionOption{phase: rule.Phase, limits: compiler.limits, declarationHash: fingerprint, option: conversionOption{raw: value, value: options}}
	return rule, nil
}

func (options *eventPreludeOptions) compile(compiler *expressionCompiler, path string) error {
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || options.EncoderHash == "" || strings.TrimSpace(options.Prelude.Reason) == "" || options.Prelude.When.Op == "" {
		return fmt.Errorf("%s: requires a pinned target event encoder, explicit condition and reason", path)
	}
	scope := expressionScope{coreInput: reflect.TypeFor[Event](), canReadOperation: true}
	var err error
	options.when, err = compileWirePredicateInScope(&options.Prelude.When, path+"/when", scope, compiler, 1)
	if err != nil {
		return err
	}
	options.response, err = compiler.compilePlan(options.Prelude.Response, path+"/response", scope, 1)
	if err != nil {
		return err
	}
	if !isAssignable(options.response.expression.result, ObjectType) {
		return fmt.Errorf("%s/response: requires a response header object", path)
	}
	return nil
}

func compileEventPreludeOption(rule ConversionRule, limits Limits) (conversionOption, error) {
	if rule.Phase != ConversionEvent || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" || rule.Match.Path != "" || rule.Match.Present != nil {
		return conversionOption{}, fmt.Errorf("event_prelude requires an event declaration with route conditions only")
	}
	if prepared := rule.preparedOption; prepared != nil && prepared.phase == rule.Phase && prepared.limits == limits && prepared.option.raw == rule.Value {
		fingerprint, err := conversionRuleFingerprint(rule)
		if err != nil {
			return conversionOption{}, err
		}
		if fingerprint == prepared.declarationHash {
			return prepared.option, nil
		}
	}
	var options eventPreludeOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if err := options.Limits.Validate(); err != nil {
		return conversionOption{}, err
	}
	compiler := newExpressionCompiler(intersectControlLimits(limits, options.Limits), options.Expressions)
	if err := options.compile(compiler, "/rules/"+rule.ID+"/value/prelude"); err != nil {
		return conversionOption{}, err
	}
	for name := range options.Expressions {
		if !compiler.used[name] {
			return conversionOption{}, fmt.Errorf("prelude expression %q is unreachable", name)
		}
	}
	compiler.releaseGraph()
	options.Prelude.When, options.Prelude.Response, options.Expressions = Expression{}, Expression{}, nil
	return conversionOption{raw: rule.Value, value: options}, nil
}

func (conversion *CompiledConversion) prependResponse(ctx context.Context, events []Event, route ConversionContext) ([]Event, bool, error) {
	for index, event := range events {
		switch event.Type {
		case ResponseStarted, OperationFailed, OperationCancelled:
			return events, true, nil
		case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished, ResponseFinished:
		default:
			continue
		}
		for _, rule := range conversion.preludes {
			if !rule.Match.matchesRoute(route) {
				continue
			}
			prelude, err := conversion.createResponsePrelude(ctx, event, route, rule)
			if err != nil {
				return nil, false, err
			}
			if prelude == nil {
				continue
			}
			result := make([]Event, 0, len(events)+1)
			result = append(result, events[:index]...)
			result = append(result, *prelude)
			result = append(result, events[index:]...)
			return result, true, nil
		}
	}
	return events, false, nil
}

func (conversion *CompiledConversion) createResponsePrelude(ctx context.Context, event Event, route ConversionContext, rule ConversionRule) (*Event, error) {
	options := conversion.options[rule.ID].value.(eventPreludeOptions)
	if options.Target != route.Target || event.SchemaVersion != options.CoreVersion {
		return nil, streamIssue(InvalidInput, "/prelude", "response prelude does not match the pinned target and core version")
	}
	contextValue, err := EncodeValue(struct {
		Model     string    `json:"model"`
		Transport Transport `json:"transport"`
	}{route.Model, route.Transport})
	if err != nil {
		return nil, err
	}
	read := func(pointer []string) (JSONOutput, error) { return readCoreEventOutput(&event, pointer, nil) }
	limits := intersectControlLimits(conversion.phaseLimits(ctx, ConversionEvent), semanticLimits(options.Limits))
	state := evaluation{ctx: ctx, inputReader: read, rootReader: read, context: contextValue, valueReader: &valueReader{},
		objectReader: newPlanObjectReader(contextValue, objectReuseReads), runtime: evaluationRuntime{isBound: true, operation: route.operation},
		budget: &evaluationBudget{limits: limits, stepLimit: min(conversion.limits.Nodes, options.Limits.Nodes)}}
	condition, err := options.when.evaluate(state)
	if err != nil {
		return nil, err
	}
	shouldGenerate, err := readBoolean(condition)
	if err != nil || !shouldGenerate {
		return nil, err
	}
	output, err := options.response.build(state)
	if err != nil {
		return nil, err
	}
	if err := checkPlannedOutputLimits(output, limits, "event_prelude"); err != nil {
		return nil, err
	}
	value, err := output.materialize(ctx)
	if err != nil {
		return nil, err
	}
	if value.IsNull() {
		return nil, streamIssue(InvalidInput, "/prelude/response", "prelude requires a response header object")
	}
	var header eventPreludeHeader
	if err := decodeContract(value.Bytes(), &header); err != nil {
		return nil, err
	}
	response := &Response{SchemaVersion: options.CoreVersion, Source: route.Target, ID: header.ID, Model: header.Model, Attributes: header.Attributes}
	prelude := &Event{SchemaVersion: options.CoreVersion, Type: ResponseStarted, Source: route.Target, ResponseID: header.ID, Response: response,
		prelude: &ConversionIssue{Code: ConversionNormalized, Severity: SeverityInfo, Fidelity: "preserved", Protocol: route.Target, Direction: EncodeEvent,
			Stage: "conversion.event", Path: "/response", Reason: options.Prelude.Reason, RuleID: rule.ID, PolicyHash: conversion.Hash,
			PolicyRevision: conversion.RuleRevisions[rule.ID], Evidence: conversion.Origins[rule.ID]}}
	if err := checkSemanticValue(*prelude, limits); err != nil {
		return nil, err
	}
	return prelude, nil
}
