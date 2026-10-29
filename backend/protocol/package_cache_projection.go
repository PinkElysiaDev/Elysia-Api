package protocol

import (
	"context"
	"fmt"
	"strings"
)

const cacheIntentScope = "cache_intents"

type cacheProjectionSpec struct {
	CoreVersion int                      `json:"coreVersion"`
	Target      Identity                 `json:"target"`
	MappingHash string                   `json:"mappingHash"`
	Native      bool                     `json:"native"`
	Effects     []ProtocolSemanticEffect `json:"effects"`
	Expressions map[string]Expression    `json:"expressions,omitempty"`
}

func validateCacheDisposition(direction Direction, effect ProtocolSemanticEffect) error {
	if effect.Kind != "drop" && effect.Kind != "default" {
		if effect.Scope != "" || !effect.Default.IsZero() || effect.Severity != "" {
			return fmt.Errorf("scope, default and severity require a cache disposition")
		}
		return nil
	}
	if direction != EncodeRequest || effect.Scope != cacheIntentScope {
		return fmt.Errorf("semantic effect %q is not executable without encode_request scope cache_intents", effect.Kind)
	}
	if effect.Path != "" || effect.Target != "" || effect.Layout != nil || effect.When == nil || strings.TrimSpace(effect.Reason) == "" {
		return fmt.Errorf("cache disposition requires a local when and reason, without path, target or layout")
	}
	if effect.Kind == "default" {
		if !effect.Default.IsNull() || effect.Severity != "" {
			return fmt.Errorf("cache default supports only explicit null without an authored severity")
		}
	} else if !effect.Default.IsZero() || (effect.Severity != SeverityInfo && effect.Severity != SeverityWarning) {
		return fmt.Errorf("cache drop requires severity info or warning and no default")
	}
	return nil
}

func localCacheCondition(plan *compiledOutputPlan) bool {
	if plan == nil {
		return true
	}
	if plan.expression.op == "read" && plan.expression.from != "input" && plan.expression.from != "root" && plan.expression.from != "item" {
		return false
	}
	for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
		if !localCacheCondition(child) {
			return false
		}
	}
	for _, child := range plan.items {
		if !localCacheCondition(child) {
			return false
		}
	}
	for _, field := range plan.fields {
		if !localCacheCondition(field.expression) {
			return false
		}
	}
	for _, child := range plan.cases {
		if !localCacheCondition(child) {
			return false
		}
	}
	return true
}

func (compiled *Compiled) bindCacheProjection(document ProtocolPackage) error {
	spec := cacheProjectionSpec{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, MappingHash: compiled.mappings[EncodeRequest].definitionHash, Native: compiled.native.Preserve, Expressions: map[string]Expression{}}
	for index, effect := range compiled.packagePlan.effects[EncodeRequest] {
		if effect.scope != cacheIntentScope {
			continue
		}
		spec.Effects = append(spec.Effects, document.Mapping.Directions[EncodeRequest].Effects[index])
		for name, expression := range effect.conditionReferences {
			spec.Expressions[name] = expression
		}
	}
	if len(spec.Effects) == 0 {
		return nil
	}
	value, err := EncodeValue(requestProjectionOptions{Core: &spec})
	compiled.packagePlan.requestProjection = value
	for index := range compiled.packagePlan.effects[EncodeRequest] {
		compiled.packagePlan.effects[EncodeRequest][index].conditionReferences = nil
	}
	return err
}

func emptyCacheDefault(intent CacheIntent) bool {
	return (intent.Kind == "key" || intent.Kind == "retention" || intent.Kind == "breakpoint") && intent.Value.IsNull() && intent.TTL.IsZero() && intent.Resource == nil
}

func hasRequestCacheIntents(request *Request) bool {
	if len(request.Cache) != 0 {
		return true
	}
	for _, tool := range request.Tools {
		if len(tool.Cache) != 0 {
			return true
		}
	}
	var hasNodes func([]Node) bool
	hasNodes = func(nodes []Node) bool {
		for _, node := range nodes {
			if len(node.Cache) != 0 || hasNodes(node.Children) || hasNodes(node.ReasoningContent) {
				return true
			}
		}
		return false
	}
	return hasNodes(request.Content)
}

func matchCacheDisposition(ctx context.Context, intent CacheIntent, effect compiledSemanticEffect, budget *evaluationBudget) (bool, error) {
	if effect.kind == "default" && !emptyCacheDefault(intent) {
		return false, nil
	}
	read := func(pointer []string) (JSONOutput, error) { return readCoreCacheIntent(intent, pointer) }
	condition, err := effect.when.evaluate(evaluation{ctx: ctx, inputReader: read, rootReader: read, budget: budget})
	if err != nil {
		return false, err
	}
	return readBoolean(condition)
}

// Encoding verifies that the selected conversion policy disposed of unsupported
// intent. It never repeats the projection behind a disabled or overridden rule.
func (compiled *Compiled) checkCacheDispositions(state evaluation, limits Limits) error {
	request := state.coreRequest
	if request == nil {
		var err error
		request, err = decodeCoreRequestOutput(JSONLeaf(state.input))
		if err != nil {
			return err
		}
		if err := validateRequestHints(request); err != nil {
			return err
		}
	}
	// Rule values are editable. The target's native policy is the authority for
	// replay, including after a conversion rule has already removed the intent.
	if compiled.native.Preserve && request.Native != nil && CanPreserveNative(request.Native.Source, compiled.target(EncodeRequest, EvaluationContext{})) {
		return streamIssue(UnsupportedNative, "/native", "cache dispositions have no proven deletion contract for same-wire native replay")
	}
	if !hasRequestCacheIntents(request) {
		return nil
	}
	budget := &evaluationBudget{limits: limits, stepLimit: limits.Nodes}
	return requestCaches(request, func(intents *[]CacheIntent, at string) error {
		for index, intent := range *intents {
			for _, effect := range compiled.packagePlan.effects[EncodeRequest] {
				if effect.scope != cacheIntentScope {
					continue
				}
				isMatch, err := matchCacheDisposition(state.ctx, intent, effect, budget)
				if err != nil {
					return err
				}
				if isMatch {
					return streamIssue(UnsupportedCapability, fmt.Sprintf("%s/%d", at, index), "cache intent requires the selected request-projection policy before encoding")
				}
			}
		}
		return nil
	})
}
