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
	for _, variant := range []string{"default", "after", "json-rule"} {
		b.Run(variant, func(b *testing.B) {
			definitions, err := Definitions()
			if err != nil {
				b.Fatal(err)
			}
			compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
			if err != nil {
				b.Fatal(err)
			}
			var compiled *p.Compiled
			for _, definition := range definitions {
				candidate, issues := compiler.Compile(definition.Bytes())
				if err := p.IssuesError(issues); err != nil {
					b.Fatal(err)
				}
				if candidate.Codec(p.EncodeRequest) == Anthropic {
					compiled = candidate
					break
				}
			}
			if compiled == nil {
				b.Fatal("missing shipped Anthropic module")
			}
			if variant == "after" {
				definition := compiled.Definition()
				for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
					mapping := definition.Directions[direction]
					mapping.After = &p.Expression{Op: "read", Path: ""}
					definition.Directions[direction] = mapping
				}
				compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
				if err != nil {
					b.Fatal(err)
				}
				value, _ := p.EncodeValue(definition)
				var issues []p.ConversionIssue
				compiled, issues = compiler.Compile(value.Bytes())
				if compiled == nil {
					b.Fatal(issues)
				}
			}
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
				request, err := compiled.DecodeRequest(b.Context(), body, p.EvaluationContext{})
				if err != nil {
					b.Fatal(err)
				}
				request, err = conversion.Request(b.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
				if err != nil {
					b.Fatal(err)
				}
				wire, err := compiled.EncodeRequest(b.Context(), request, p.EvaluationContext{})
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
	value, err := p.ParseValue([]byte(`{"content":[{"token":"x","logprob":-0.5,"bytes":[120],"top_logprobs":[]}]}`))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			delta := []p.ResponseMetadata{{Codec: "openai-chat", Location: "choice", Name: "logprobs", Value: value}}
			b.ReportAllocs()
			for b.Loop() {
				var current []p.ResponseMetadata
				for range count {
					current = p.MergeNodeMetadata(current, delta, true)
				}
				fields, err := current[0].Value.ReadObject()
				var entries []p.Value
				if err != nil || fields["content"].Decode(&entries) != nil || len(entries) != count {
					b.Fatal("metadata accumulation lost entries")
				}
			}
		})
	}
}
