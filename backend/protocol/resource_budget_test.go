package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResourceBudgetBoundaries(t *testing.T) {
	value := parseTestValue(t, `{"a":[1]}`)
	limits := DefaultLimits()
	limits.BufferBytes, limits.Nodes, limits.Depth = len(value.raw), 6, 2
	if err := checkResourceValue(value, limits, "request_wire"); err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{"bytes", "nodes", "depth"} {
		short := limits
		switch metric {
		case "bytes":
			short.BufferBytes--
		case "nodes":
			short.Nodes--
		case "depth":
			short.Depth--
		}
		var failure *ResourceLimitError
		if err := checkResourceValue(value, short, "request_wire"); !errors.As(err, &failure) || failure.Metric != metric || failure.Domain != "request_wire" {
			t.Fatalf("%s: %v", metric, err)
		}
	}
}

func TestResourceBudgetSnapshotAndOverflow(t *testing.T) {
	ctx, err := WithRequestBodyLimit(t.Context(), 48<<20)
	if err != nil {
		t.Fatal(err)
	}
	next, err := WithRequestBodyLimit(ctx, 1<<20)
	if err != nil || RequestBodyLimit(next) != 48<<20 {
		t.Fatal("in-flight budget changed")
	}
	if _, err := WithRequestBodyLimit(t.Context(), int64(^uint64(0)>>1)); err == nil {
		t.Fatal("overflow accepted")
	}
	wire := requestLimits(ctx, DefaultLimits(), false)
	work := semanticLimits(wire)
	if work.BufferBytes != 384<<20 || work.Nodes != 1600000 || work.Depth != 256 {
		t.Fatal(work)
	}
	if got := requestLimits(ctx, DefaultLimits(), true); got.BufferBytes != 8<<20 {
		t.Fatal("authored limit discarded", got)
	}
}

func TestSemanticBudgetPreflightAndExpansion(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 100
	request := &Request{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("x", 101))}}}
	var failure *ResourceLimitError
	if err := checkSemanticValue(request, limits); !errors.As(err, &failure) || failure.Domain != "semantic" {
		t.Fatal(err)
	}
	policy := ConversionPolicy{SchemaVersion: 1, ID: "small", Limits: &limits, Rules: []ConversionRule{{ID: "expand", Enabled: true, Phase: ConversionRequest, Action: "set", Path: "/parameters", Value: parseTestValue(t, `{"x":"`+strings.Repeat("x", 801)+`"}`)}}}
	c, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Request(t.Context(), &Request{SchemaVersion: 1, Content: []Node{}}, ConversionContext{}, nil)
	if !errors.As(err, &failure) {
		t.Fatalf("unbounded expansion: %v", err)
	}
	var conversion *ConversionError
	if !errors.As(err, &conversion) || conversion.Issues[0].RuleID != "expand" || conversion.Issues[0].PolicyHash != c.Hash || conversion.Issues[0].Direction != DecodeRequest {
		t.Fatal("resource provenance lost", err)
	}
}

func TestRequestBudgetDoesNotExpandResponsesOrExecution(t *testing.T) {
	ctx, err := WithRequestBodyLimit(t.Context(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &Compiled{limits: DefaultLimits()}
	if compiled.wireLimits(ctx, DecodeResponse).BufferBytes != 8<<20 || compiled.wireLimits(ctx, DecodeEvent).BufferBytes != 8<<20 {
		t.Fatal("response boundary expanded")
	}
	if compiled.wireLimits(ctx, DecodeRequest).BufferBytes != 64<<20 {
		t.Fatal("request boundary not expanded")
	}
	policy, err := ResolveConversion(ConversionPolicy{SchemaVersion: 1, ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ruleLimits(ctx, ConversionWire, ConversionContext{ResponseWire: true}).BufferBytes != 8<<20 {
		t.Fatal("response wire budget leaked")
	}
	budget := &evaluationBudget{limits: semanticLimits(DefaultLimits()), stepLimit: 1}
	expression := compiledExpression{op: "literal", value: StringValue("x")}
	state := evaluation{ctx: ctx, budget: budget}
	if _, err := expression.evaluate(state); err != nil {
		t.Fatal(err)
	}
	var resource *ResourceLimitError
	if _, err := expression.evaluate(state); !errors.As(err, &resource) || resource.Metric != "steps" {
		t.Fatal(err)
	}
	cyclic := &Request{SchemaVersion: 1, Content: make([]Node, 1)}
	cyclic.Content[0].Children = cyclic.Content
	if err := checkSemanticValue(cyclic, semanticLimits(DefaultLimits())); !errors.As(err, &resource) {
		t.Fatal("cycle was not bounded", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := expression.evaluate(evaluation{ctx: canceled, budget: &evaluationBudget{limits: DefaultLimits()}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestConversionRequestHonorsCancellation(t *testing.T) {
	conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: 1, ID: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = conversion.Request(ctx, &Request{SchemaVersion: 1, Content: []Node{}}, ConversionContext{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
