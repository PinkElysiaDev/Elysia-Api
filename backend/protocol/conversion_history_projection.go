package protocol

import (
	"context"
	"fmt"
	"strings"
)

const requestFieldScope = "request_field"

func validateHistoryDisposition(direction Direction, effect ProtocolSemanticEffect) error {
	if direction != EncodeRequest || effect.Path != historyRetentionPath || (effect.Kind != "default" && effect.Kind != "reject") || effect.Target != "" || effect.Layout != nil || effect.ValueMapping != nil || effect.When == nil || effect.Severity != "" || strings.TrimSpace(effect.Reason) == "" {
		return fmt.Errorf("request_field supports only explicit historyRetention default or reject dispositions")
	}
	if (effect.Kind == "default" && !effect.Default.IsString("all")) || (effect.Kind == "reject" && !effect.Default.IsZero()) {
		return fmt.Errorf("history default must explicitly declare all; reject has no default")
	}
	return nil
}

func (c *CompiledConversion) projectRequestIntents(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledRequestProjection, sink *DiagnosticSink) error {
	if plan == nil {
		return fmt.Errorf("core projection requires a compiled rule")
	}
	if route.Target != plan.spec.Target || request.SchemaVersion != plan.spec.CoreVersion {
		return streamIssue(InvalidInput, "/", "projection does not match the pinned target and core version")
	}
	if !hasRequestCacheIntents(request) && (request.Reasoning == nil || request.Reasoning.HistoryRetention.IsZero()) {
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
			err = c.projectHistoryRetention(ctx, request, route, rule, &part, sink, budget)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func historyDispositionMatches(ctx context.Context, value Value, effect compiledSemanticEffect, budget *evaluationBudget) (bool, error) {
	if value.IsZero() {
		return false, nil
	}
	if !value.IsString("all") {
		return false, streamIssue(InvalidInput, historyRetentionPath, "history retention must be all when present")
	}
	condition, err := effect.when.evaluate(evaluation{ctx: ctx, input: value, root: value, budget: budget})
	if err != nil {
		return false, err
	}
	return readBoolean(condition)
}

func (c *CompiledConversion) projectHistoryRetention(ctx context.Context, request *Request, route ConversionContext, rule ConversionRule, plan *compiledRequestProjection, sink *DiagnosticSink, budget *evaluationBudget) error {
	if request.Reasoning == nil {
		return nil
	}
	for _, effect := range plan.effects {
		isMatch, err := historyDispositionMatches(ctx, request.Reasoning.HistoryRetention, effect, budget)
		if err != nil {
			return err
		}
		if !isMatch {
			continue
		}
		if effect.kind == "reject" {
			rejected := rule
			rejected.Action = "reject"
			return c.issue(rejected, ConversionRequest, route, historyRetentionPath, effect.reason, sink, false)
		}
		if plan.spec.Native && request.Native != nil && CanPreserveNative(request.Native.Source, Target{Protocol: plan.spec.Target, Direction: EncodeRequest}) {
			return c.requestProjectionError(rule, route, historyRetentionPath, UnsupportedNative, "history default has no proven deletion contract for same-wire native replay")
		}
		request.Reasoning.HistoryRetention = Value{}
		if *request.Reasoning == (ReasoningIntent{}) {
			request.Reasoning = nil
		}
		c.normalized(rule, route, historyRetentionPath, effect.reason, sink)
		return nil
	}
	return nil
}

func (compiled *Compiled) checkHistoryDispositions(state evaluation, limits Limits) error {
	request := state.coreRequest
	if request == nil {
		var err error
		request, err = decodeCoreRequestOutput(JSONLeaf(state.input))
		if err != nil {
			return err
		}
	}
	if compiled.native.Preserve && request.Native != nil && CanPreserveNative(request.Native.Source, compiled.target(EncodeRequest, EvaluationContext{})) {
		baseline := compiled.requestReplayBaseline(request.Native, compiled.wireLimits(state.ctx, DecodeRequest))
		if baseline == nil {
			return streamIssue(UnsupportedNative, "/native", "history dispositions require a matching private baseline for native replay")
		}
		if err := compiled.checkHistoryDispositionMatches(state.ctx, baseline, limits, true); err != nil {
			return err
		}
	}
	return compiled.checkHistoryDispositionMatches(state.ctx, request, limits, false)
}

func (compiled *Compiled) checkHistoryDispositionMatches(ctx context.Context, request *Request, limits Limits, native bool) error {
	if request.Reasoning == nil || request.Reasoning.HistoryRetention.IsZero() {
		return nil
	}
	budget := &evaluationBudget{limits: limits, stepLimit: limits.Nodes}
	for _, effect := range compiled.packagePlan.effects[EncodeRequest] {
		if effect.scope != requestFieldScope {
			continue
		}
		isMatch, err := historyDispositionMatches(ctx, request.Reasoning.HistoryRetention, effect, budget)
		if err != nil {
			return err
		}
		if isMatch {
			if native {
				return streamIssue(UnsupportedNative, "/native", "original history intent has no proven deletion contract for native replay")
			}
			return streamIssue(UnsupportedCapability, historyRetentionPath, "history intent requires the selected request-projection policy before encoding")
		}
	}
	return nil
}
