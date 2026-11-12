package protocol

import (
	"context"
	"reflect"
)

// normalizeDocumentMapping reuses only immutable spelling facts from a complete
// document measurement. Mutable containers still acquire independent ownership;
// normalization output and policy verdicts never enter the document cache.
func normalizeDocumentMapping[T any](ctx context.Context, input T, limits Limits) T {
	owner, _ := ctx.Value(documentMetricsKey{}).(*documentMetrics)
	if owner == nil {
		return normalizeMappingSemantic(input, limits)
	}
	value := reflect.ValueOf(input)
	plan := typedLimitPlans[value.Type()]
	owned := cloneSemantic(value, plan)
	owner.mu.Lock()
	defer owner.mu.Unlock()
	cache := semanticCache{limits: limits, normalizationFacts: &owner.cache}
	cache.normalizeDocument(owned, plan)
	return owned.Interface().(T)
}
