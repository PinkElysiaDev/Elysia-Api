package protocol

import (
	"context"
	"fmt"
	"strings"
)

const requestFieldScope = "request_field"

func validateRequestFieldDisposition(direction Direction, effect ProtocolSemanticEffect) error {
	if direction != EncodeRequest || (effect.Kind != "default" && effect.Kind != "reject") || effect.Target != "" || effect.Layout != nil || effect.ValueMapping != nil || effect.When == nil || effect.Severity != "" || strings.TrimSpace(effect.Reason) == "" {
		return fmt.Errorf("request_field requires an explicit default or reject disposition")
	}
	if (effect.Kind == "default" && effect.Default.IsZero()) || (effect.Kind == "reject" && !effect.Default.IsZero()) {
		return fmt.Errorf("default requires a declared value; reject has no default")
	}
	_, err := compileRequestFieldClear(effect.Path)
	return err
}

func (c *CompiledConversion) projectRequestIntents(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledRequestProjection, sink *DiagnosticSink) error {
	if plan == nil {
		return fmt.Errorf("core projection requires a compiled rule")
	}
	if route.Target != plan.spec.Target || request.SchemaVersion != plan.spec.CoreVersion {
		return streamIssue(InvalidInput, "/", "projection does not match the pinned target and core version")
	}
	if !hasRequestCacheIntents(request) && !hasRequestFieldIntents(request, plan.effects) {
		return nil
	}
	budget := &evaluationBudget{limits: c.phaseLimits(ctx, ConversionRequest), stepLimit: c.limits.Nodes}
	for _, group := range plan.groups {
		part := *plan
		part.effects = plan.effects[group.start:group.end]
		var err error
		if group.scope == cacheIntentScope {
			err = c.projectCacheIntentGroup(ctx, request, route, rule, &part, sink, budget)
		} else {
			err = c.projectRequestFields(ctx, request, route, rule, &part, sink, budget)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func requestFieldDispositionMatches(ctx context.Context, request *Request, effect compiledSemanticEffect, budget *evaluationBudget) (bool, error) {
	output, err := readCoreRequest(*request, effect.corePointer, nil)
	if err != nil {
		return false, err
	}
	if output.IsZero() {
		return false, nil
	}
	value := output.value
	condition, err := effect.when.evaluate(evaluation{ctx: ctx, input: value, root: value, budget: budget})
	if err != nil {
		return false, err
	}
	isMatch, err := readBoolean(condition)
	if err != nil || !isMatch {
		return false, err
	}
	if effect.kind == "default" && !equalValues(value, effect.defaultValue) {
		return false, streamIssue(UnsupportedCapability, effect.path, "field differs from the declared equivalent default")
	}
	return true, nil
}

func (c *CompiledConversion) projectRequestFields(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledRequestProjection, sink *DiagnosticSink, budget *evaluationBudget) error {
	for _, effect := range plan.effects {
		isMatch, err := requestFieldDispositionMatches(ctx, request, effect, budget)
		if err != nil {
			return err
		}
		if !isMatch {
			continue
		}
		if effect.kind == "reject" {
			rejected := rule
			rejected.Action = "reject"
			return c.issue(rejected, ConversionRequest, route, effect.path, effect.reason, sink, false)
		}
		if plan.spec.Native && request.Native != nil && CanPreserveNative(request.Native.Source, Target{Protocol: plan.spec.Target, Direction: EncodeRequest}) {
			return c.requestProjectionError(rule, route, effect.path, UnsupportedNative, "field default has no proven deletion contract for same-wire native replay")
		}
		effect.clearField(request)
		c.normalized(rule, route, effect.path, effect.reason, sink)
	}
	return nil
}

func (compiled *Compiled) checkRequestFieldDispositions(state evaluation, limits Limits, options EvaluationContext) error {
	request := state.coreRequest
	if request == nil {
		var err error
		request, err = decodeCoreRequestOutput(JSONLeaf(state.input))
		if err != nil {
			return err
		}
	}
	if compiled.native.Preserve && request.Native != nil && CanPreserveNative(request.Native.Source, compiled.target(EncodeRequest, options)) {
		replay := options.requestReplay
		if replay == nil {
			// Raw authoring has no later native reconciliation, so its resolver
			// remains local to this preflight instead of escaping with the value.
			replay = newRequestReplay(compiled, options)
		}
		prepared, err := replay.load(state.ctx, request.Native)
		if err != nil {
			return err
		}
		if err := compiled.checkRequestFieldDispositionMatches(state.ctx, prepared.baseline, limits, true); err != nil {
			return err
		}
	}
	return compiled.checkRequestFieldDispositionMatches(state.ctx, request, limits, false)
}

func (compiled *Compiled) checkRequestFieldDispositionMatches(ctx context.Context, request *Request, limits Limits, native bool) error {
	if !hasRequestFieldIntents(request, compiled.packagePlan.effects[EncodeRequest]) {
		return nil
	}
	budget := &evaluationBudget{limits: limits, stepLimit: limits.Nodes}
	for _, effect := range compiled.packagePlan.effects[EncodeRequest] {
		if effect.scope != requestFieldScope {
			continue
		}
		isMatch, err := requestFieldDispositionMatches(ctx, request, effect, budget)
		if err != nil {
			return err
		}
		if isMatch {
			if native {
				return streamIssue(UnsupportedNative, "/native", "original field intent has no proven deletion contract for native replay")
			}
			return streamIssue(UnsupportedCapability, effect.path, "field intent requires the selected request-projection policy before encoding")
		}
	}
	return nil
}

func hasRequestFieldIntents(request *Request, effects []compiledSemanticEffect) bool {
	for _, effect := range effects {
		if effect.scope == requestFieldScope {
			value, _ := readCoreRequest(*request, effect.corePointer, nil)
			if !value.IsZero() {
				return true
			}
		}
	}
	return false
}
