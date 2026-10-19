package protocol_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type ruleCancellationContext struct {
	context.Context
	checks int
	cancel context.CancelFunc
}

func (ctx *ruleCancellationContext) Err() error {
	ctx.checks++
	// Ingress entry/exit, request entry and the first action finish before the
	// next rule guard observes cancellation; this is not a pre-cancelled input.
	if ctx.checks == 5 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestConversionCancellationBetweenRules(t *testing.T) {
	for _, compiled := range performanceProtocols(t) {
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
		if err != nil {
			t.Fatal(err)
		}
		request := &p.Request{SchemaVersion: 1, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("keep")}}}}}
		before, _ := p.EncodeValue(request)
		route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
		for _, mode := range []string{"baseline", "optimized"} {
			base, cancel := context.WithCancel(t.Context())
			ctx := &ruleCancellationContext{Context: base, cancel: cancel}
			var result *p.Request
			if mode == "baseline" {
				result, err = conversion.RequestBaseline(ctx, request, route, nil)
			} else {
				result, err = conversion.Request(ctx, request, route, nil)
			}
			cancel()
			after, _ := p.EncodeValue(request)
			if !errors.Is(err, context.Canceled) || result != nil || before != after || ctx.checks < 5 {
				t.Fatalf("%s cancellation: result=%v error=%v checks=%d", mode, result, err, ctx.checks)
			}
		}
	}
}

// Branch workloads retain complete default policies and compare their results before timing.
func BenchmarkConversionActiveWorkloads(b *testing.B) {
	protocols := map[string]*p.Compiled{}
	for _, compiled := range performanceProtocols(b) {
		protocols[compiled.Codec(p.EncodeRequest)] = compiled
	}
	smallNodes := make([]p.Node, 1800)
	for index := range smallNodes {
		smallNodes[index] = p.Node{Kind: p.TextNode, Payload: p.StringValue("small tool result")}
	}
	history := make([]p.Node, 0, 1800)
	for range 600 {
		history = append(history, p.Node{Kind: p.ReasoningNode, Payload: p.StringValue("reason")}, p.Node{Kind: p.TextNode, Payload: p.StringValue("answer")}, p.Node{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("next")}}})
	}
	signed := p.Node{Kind: p.ReasoningNode, Payload: p.StringValue(strings.Repeat("x", 2<<20)), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("synthetic")}}}
	cases := []struct {
		name, source, target string
		request              *p.Request
		recoverable          map[string]bool
	}{
		{"nodes1800", "anthropic", "anthropic", &p.Request{SchemaVersion: 1, Content: []p.Node{{Kind: p.ToolResultNode, CallID: p.StringValue("c"), Children: smallNodes}}}, nil},
		{"history1800", "responses", "anthropic", &p.Request{SchemaVersion: 1, Content: history}, nil},
		{"gemini_wrap2MiB", "anthropic", "gemini", &p.Request{SchemaVersion: 1, Content: []p.Node{{Kind: p.ToolResultNode, CallID: p.StringValue("c"), Payload: p.StringValue(strings.Repeat("x", 2<<20))}}}, nil},
		{"signature_drop2MiB", "anthropic", "responses", &p.Request{SchemaVersion: 1, Content: []p.Node{signed}}, nil},
		{"signature_key2MiB", "anthropic", "responses", &p.Request{SchemaVersion: 1, Content: []p.Node{signed}}, map[string]bool{p.ContinuationResourceKey(signed.Resources[0]): true}},
		{"signature_digest2MiB", "anthropic", "responses", &p.Request{SchemaVersion: 1, Content: []p.Node{signed}}, map[string]bool{p.ContinuationNodeDigest(signed): true}},
	}
	for _, tc := range cases {
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(protocols[tc.source], protocols[tc.target]))
		if err != nil {
			b.Fatal(err)
		}
		route := p.ConversionContext{Source: protocols[tc.source].Identity(), Target: protocols[tc.target].Identity(), Recoverable: tc.recoverable}
		oldSink, newSink := &p.DiagnosticSink{}, &p.DiagnosticSink{}
		old, oldErr := conversion.RequestBaseline(b.Context(), tc.request, route, oldSink)
		current, newErr := conversion.Request(b.Context(), tc.request, route, newSink)
		if oldErr != nil || newErr != nil || !reflect.DeepEqual(old, current) || !reflect.DeepEqual(oldSink.Issues(), newSink.Issues()) {
			b.Fatalf("%s differential mismatch old=%v new=%v", tc.name, oldErr, newErr)
		}
		for _, mode := range []string{"baseline", "optimized"} {
			b.Run(fmt.Sprintf("%s/%s", tc.name, mode), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var err error
					if mode == "baseline" {
						_, err = conversion.RequestBaseline(b.Context(), tc.request, route, nil)
					} else {
						_, err = conversion.Request(b.Context(), tc.request, route, nil)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
