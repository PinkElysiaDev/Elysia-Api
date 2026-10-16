package protocol

import (
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
}
