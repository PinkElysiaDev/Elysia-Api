package protocol

import (
	"context"
	"reflect"
	"testing"
)

func TestIdentityAfterChecksMatchEvaluation(t *testing.T) {
	expression := &compiledExpression{op: "read", from: "input", path: "/directions/encode_request/after"}
	value := StringValue("payload")
	for _, canceled := range []bool{false, true} {
		for _, size := range []int{len(value.raw) - 1, len(value.raw), len(value.raw) + 1} {
			ctx, cancel := context.WithCancel(t.Context())
			if canceled {
				cancel()
			}
			limits := DefaultLimits()
			limits.BufferBytes = size
			state := evaluation{ctx: ctx, input: value, budget: &evaluationBudget{limits: limits, stepLimit: 1}}
			_, before := expression.evaluate(state)
			after := expression.checkIdentityAfter(ctx, len(value.raw), limits)
			cancel()
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("canceled=%t, bytes=%d: generic %#v, identity %#v", canceled, size, before, after)
			}
		}
	}
}
