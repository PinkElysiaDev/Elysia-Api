package protocol

import "fmt"

func isRequestProjectionScope(scope string) bool {
	return scope == cacheIntentScope || scope == requestFieldScope
}

func validateRequestProjectionEffect(direction Direction, effect ProtocolSemanticEffect) error {
	if effect.Diagnostic != nil && (effect.Kind != "reject" || effect.Scope != "") {
		return fmt.Errorf("diagnostic requires an unscoped reject effect")
	}
	if effect.Scope == requestFieldScope {
		return validateHistoryDisposition(direction, effect)
	}
	return validateCacheDisposition(direction, effect)
}

type requestProjectionSpec struct {
	CoreVersion int                      `json:"coreVersion"`
	Target      Identity                 `json:"target"`
	MappingHash string                   `json:"mappingHash"`
	Native      bool                     `json:"native"`
	Effects     []ProtocolSemanticEffect `json:"effects"`
	Expressions map[string]Expression    `json:"expressions,omitempty"`
}

func localProjectionCondition(plan *compiledOutputPlan) bool {
	if plan == nil {
		return true
	}
	if plan.expression.op == "read" && plan.expression.from != "input" && plan.expression.from != "root" && plan.expression.from != "item" && plan.expression.from != "binding" {
		return false
	}
	for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
		if !localProjectionCondition(child) {
			return false
		}
	}
	for _, child := range plan.items {
		if !localProjectionCondition(child) {
			return false
		}
	}
	for _, field := range plan.fields {
		if !localProjectionCondition(field.expression) {
			return false
		}
	}
	for _, child := range plan.cases {
		if !localProjectionCondition(child) {
			return false
		}
	}
	return true
}

func (compiled *Compiled) bindRequestProjection(document ProtocolPackage) error {
	spec := requestProjectionSpec{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, MappingHash: compiled.mappings[EncodeRequest].definitionHash, Native: compiled.native.Preserve, Expressions: map[string]Expression{}}
	for index, effect := range compiled.packagePlan.effects[EncodeRequest] {
		if !isRequestProjectionScope(effect.scope) {
			continue
		}
		compiled.packagePlan.hasCacheProjection = compiled.packagePlan.hasCacheProjection || effect.scope == cacheIntentScope
		compiled.packagePlan.hasHistoryProjection = compiled.packagePlan.hasHistoryProjection || effect.scope == requestFieldScope
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
