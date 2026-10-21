package protocol

import (
	"context"
	"reflect"
	"sync"
)

// documentMetrics owns policy-independent facts for one synchronous document
// execution, including nested native replay. It never stores mutable semantic
// containers, authorization/schema verdicts or normalized document copies.
// A mutex also permits a trusted module to fork work using the supplied context.
type documentMetrics struct {
	mu    sync.Mutex
	cache semanticCache
}

type documentMetricsKey struct{}

// Small calls retain the allocation-free context path. Cache storage has a hard
// bound independent of an authored expansion budget and is discarded when the
// outer decode/encode call returns; it is never attached to returned Requests.
func withDocumentMetrics(ctx context.Context, size int) context.Context {
	if size < 64<<10 || ctx.Value(documentMetricsKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, documentMetricsKey{}, &documentMetrics{cache: semanticCache{limits: Limits{BufferBytes: 64 << 20, StateItems: 128}}})
}

func fitsDocumentMetrics(ctx context.Context, value any, limits Limits) bool {
	owner, _ := ctx.Value(documentMetricsKey{}).(*documentMetrics)
	if owner == nil {
		return fitsTypedLimits(value, limits)
	}
	plan := typedLimitPlans[reflect.TypeOf(value)]
	if plan == nil {
		return false
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	meter := typedMeter{limits: limits, cache: &owner.cache}
	return meter.visit(plan, reflect.ValueOf(value)) && meter.fits()
}

func checkDocumentSemantic(ctx context.Context, value any, limits Limits) error {
	if ctx.Value(documentMetricsKey{}) == nil {
		return checkSemanticValue(value, limits)
	}
	if fitsDocumentMetrics(ctx, value, limits) {
		return nil
	}
	// Re-run the established error path to preserve observed counts, paths and
	// precedence when a current budget is tighter than a cached fact.
	return checkSemanticValue(value, limits)
}

func checkDocumentMapping(ctx context.Context, value any, limits Limits, domain string) error {
	if fitsDocumentMetrics(ctx, value, limits) {
		return nil
	}
	encoded, err := EncodeValue(value)
	if err != nil {
		return err
	}
	return checkResourceValue(encoded, limits, domain)
}
