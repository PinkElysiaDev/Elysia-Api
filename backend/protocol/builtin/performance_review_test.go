package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Catalog generation belongs to editor/admin operations, not the gateway hot path.
func BenchmarkReviewSchemaCatalog(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if p.DescribeSchema().SchemaVersion == 0 {
			b.Fatal("empty catalog")
		}
	}
}

// BenchmarkReviewRequestBoundaries compares complete request conversion with
// authored synchronization boundaries. Compilation and fixtures are outside the
// measurement; all decode/conversion/encode checks remain inside each iteration.

func BenchmarkReviewRequestBoundaries(b *testing.B) {
	for _, variant := range []string{"default", "json-rule"} {
		b.Run(variant, func(b *testing.B) {
			compiled := testCompiled(b, Anthropic)
			policy := p.DefaultConversionPolicy(compiled, compiled)
			if variant == "json-rule" {
				present := true
				policy.Rules = append(policy.Rules, p.ConversionRule{ID: "review-json-boundary", Order: 55, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: "/model", Value: p.StringValue("m"), Match: p.ConversionMatch{Path: "/model", Present: &present}})
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				b.Fatal(err)
			}
			body := largeAnthropicRequest(2<<20, 0)
			b.ReportAllocs()
			for b.Loop() {
				request, err := compiled.DecodeRequest(b.Context(), body, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					b.Fatal(err)
				}
				request, err = conversion.Request(b.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
				if err != nil {
					b.Fatal(err)
				}
				wire, err := compiled.EncodeRequest(b.Context(), request, fixtureOptions(p.EvaluationContext{}))
				if err != nil || len(wire) < 2<<20 {
					b.Fatal("incomplete request", err)
				}
			}
		})
	}
}

// This deliberately isolates metadata accumulation; the HTTP logprobs fixture
// separately measures the complete stream and checks the final token count.
func BenchmarkReviewLogprobAccumulation(b *testing.B) {
	value, err := p.ParseValue([]byte(`[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]`))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			target := p.Target{Capabilities: p.CapabilitySet{p.TextCapability: true}}
			start := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}
			delta := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: start.ItemID, Delta: p.StringValue(""),
				Metadata: []p.ResponseMetadata{{Codec: "custom", Location: "content", Name: "logprobs", Value: value, Update: "append"}}}
			finish := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: start.ItemID}
			b.ReportAllocs()
			for b.Loop() {
				collector, err := p.NewResponseCollector(target, p.DefaultLimits())
				if err != nil {
					b.Fatal(err)
				}
				if _, _, err := collector.Consume(start); err != nil {
					b.Fatal(err)
				}
				for range count {
					if _, _, err := collector.Consume(delta); err != nil {
						b.Fatal(err)
					}
				}
				if _, _, err := collector.Consume(finish); err != nil {
					b.Fatal(err)
				}
				node, _, ok, err := collector.CompletedNode(finish)
				var entries []p.Value
				if err != nil || !ok || len(node.Metadata) != 1 || node.Metadata[0].Value.Decode(&entries) != nil || len(entries) != count {
					b.Fatal("metadata accumulation lost entries")
				}
			}
		})
	}
}
