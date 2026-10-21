package protocol

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestDocumentMetricsOwnershipAndBudgetParity(t *testing.T) {
	limits := semanticLimits(DefaultLimits())
	payload := StringValue(strings.Repeat("<>&\u2028", 16384))
	ctx := withDocumentMetrics(t.Context(), payload.Len())
	owner := ctx.Value(documentMetricsKey{}).(*documentMetrics)
	if withDocumentMetrics(ctx, payload.Len()) != ctx || withDocumentMetrics(t.Context(), 16) != t.Context() {
		t.Fatal("workspace lifetime/short-path changed")
	}
	request := &Request{Content: []Node{{Kind: TextNode, Payload: payload}, {Kind: TextNode, Payload: payload}}}
	for _, budget := range []Limits{limits, func() Limits { l := limits; l.BufferBytes = payload.Len() + 100; return l }(), func() Limits { l := limits; l.Depth = 1; return l }(), func() Limits { l := limits; l.Nodes = 3; return l }()} {
		for range 2 {
			if got, want := checkDocumentSemantic(ctx, request, budget), checkSemanticValue(request, budget); !reflect.DeepEqual(got, want) {
				t.Fatalf("budget verdict changed: %v / %v", got, want)
			}
			encoded, err := EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := checkDocumentMapping(ctx, request, budget, "mapping_input"), checkResourceValue(encoded, budget, "mapping_input"); !reflect.DeepEqual(got, want) {
				t.Fatalf("mapping error changed: %v / %v", got, want)
			}
		}
	}
	if owner.cache.size() != 1 {
		t.Fatal("shared immutable leaf did not reuse metrics", owner.cache.size())
	}
	request.Content[0].Payload = StringValue(strings.Repeat("changed", 20000))
	if err := checkDocumentSemantic(ctx, request, limits); err != nil {
		t.Fatal(err)
	}
	if owner.cache.size() != 2 {
		t.Fatal("mutation reused an obsolete fact")
	}
	if withDocumentMetrics(t.Context(), payload.Len()).Value(documentMetricsKey{}) == owner {
		t.Fatal("workspace crossed calls")
	}
	// Mutable containers are always traversed, even if their leaves are cached.
	request.Content = append(request.Content, request.Content...)
	small := limits
	small.BufferBytes = payload.Len()
	if err := checkDocumentSemantic(ctx, request, small); err == nil {
		t.Fatal("occurrences stopped counting")
	}
	owner.cache.limits.StateItems = 2
	if err := checkDocumentSemantic(ctx, &Node{Payload: StringValue(strings.Repeat("other", 16384))}, limits); err != nil {
		t.Fatal(err)
	}
	if owner.cache.size() != 2 || owner.cache.retainedBytes > owner.cache.limits.BufferBytes {
		t.Fatal("unbounded workspace")
	}
}

func TestDocumentMetricsConcurrentContext(t *testing.T) {
	ctx := withDocumentMetrics(t.Context(), 1<<20)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			request := &Request{Model: StringValue(fmt.Sprint(i)), Content: []Node{{Payload: StringValue(strings.Repeat("a", 65536))}}}
			for range 10 {
				if err := checkDocumentSemantic(ctx, request, semanticLimits(DefaultLimits())); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkDocumentMetrics(b *testing.B) {
	payload := StringValue(strings.Repeat("A", 2<<20))
	request := &Request{Content: []Node{{Payload: payload}, {Payload: payload}}}
	limits := semanticLimits(DefaultLimits())
	for _, reuse := range []bool{false, true} {
		b.Run(fmt.Sprint(reuse), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ctx := context.Background()
				if reuse {
					ctx = withDocumentMetrics(ctx, payload.Len())
				}
				for range 4 {
					if err := checkDocumentSemantic(ctx, request, limits); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
