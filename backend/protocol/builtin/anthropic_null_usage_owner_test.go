package builtin

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicNullUsageDetailsKeepOwnership(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []struct {
		name, details             string
		hasCreation, hasRemainder bool
	}{
		{"absent", "", false, false},
		{"cache-null", `,"cache_creation":null`, false, true},
		{"output-null", `,"output_tokens_details":null`, false, true},
		{"paired-null", `,"cache_creation":null,"output_tokens_details":null`, false, true},
		{"cache-null-zero", `,"cache_creation":null,"cache_creation_input_tokens":0`, true, true},
		{"unknown-details", `,"cache_creation":{"future":{"n":9007199254740993.25}},"output_tokens_details":{"future":false}`, false, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			usage := `{"input_tokens":10,"output_tokens":2` + fixture.details + `}`
			raw := buildExecutionResponse(usage)
			response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Usage.Input.Count != 10 || response.Usage.Output.Count != 2 || (response.Usage.CacheCreation != nil) != fixture.hasCreation {
				t.Fatal("nullable observations changed counters or missing cache total", response.Usage)
			}
			if fixture.hasCreation && (response.Usage.CacheCreation.Count != 0 || response.Usage.CacheCreation.Origin != p.ObservedCount) {
				t.Fatal("explicit zero changed its value or origin", response.Usage.CacheCreation)
			}
			for _, hasNative := range []bool{true, false} {
				candidate := *response
				if !hasNative {
					candidate.Native = nil
				}
				before, err := p.EncodeValue(candidate)
				if err != nil {
					t.Fatal(err)
				}
				checkEncodedExecutionUsage(t, compiled, &candidate, usage)
				after, err := p.EncodeValue(candidate)
				if err != nil || before != after {
					t.Fatal("reconstruction changed source observations", err)
				}
			}
			if !fixture.hasRemainder {
				return
			}
			for _, target := range []string{Chat, Responses, Gemini} {
				candidate := *response
				candidate.Native = nil
				if target == Gemini && fixture.hasCreation {
					_, err := testCompiled(t, target).EncodeResponseValue(t.Context(), &candidate, fixtureOptions(p.EvaluationContext{}))
					var failure *p.ConversionError
					if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
						return issue.Path == "/usage/cacheCreation" && issue.Code == p.ConversionRejected
					}) {
						t.Fatal("cache creation restriction lost its earlier rejection", err)
					}
					// Isolate ownership from Gemini's independent cache counter restriction.
					candidate.Usage = p.CloneUsage(response.Usage)
					candidate.Usage.CacheCreation = nil
				}
				_, err := testCompiled(t, target).EncodeResponseValue(t.Context(), &candidate, fixtureOptions(p.EvaluationContext{}))
				var failure *p.ConversionError
				if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
					return issue.Path == "/attributes" && (issue.RuleID == "response-field-owner" || issue.RuleID == "response-attribute-mapping")
				}) {
					t.Fatal("source-owned nullable observations disappeared on foreign encoding", target, err)
				}
			}
		})
	}
}

func TestAnthropicNullUsageDetailsRejectCounterConflicts(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []struct{ field, detail string }{
		{"cache_creation", "ephemeral_5m_input_tokens"},
		{"output_tokens_details", "output.reasoning_tokens"},
	} {
		t.Run(fixture.field, func(t *testing.T) {
			usage := fmt.Sprintf(`{"input_tokens":10,"output_tokens":2,%q:null}`, fixture.field)
			response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			response.Native = nil
			response.Usage.Details[fixture.detail] = p.Counter{Count: 1, Origin: p.ObservedCount}
			_, err = compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			var failure *p.ConversionError
			if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
				return issue.Path == "/usage/"+fixture.field && issue.Code == p.UnsupportedCapability
			}) {
				t.Fatal("explicit null was silently replaced by a concrete counter object", err)
			}
		})
	}
}

func TestAnthropicNullableUsageStreamCountersAndReplay(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []struct {
		name, start, delta string
		hasCreation        bool
	}{
		{"start-null", `,"cache_creation":null,"output_tokens_details":null`, "", false},
		{"delta-null", "", `,"cache_creation":null,"output_tokens_details":null`, false},
		{"both-null-zero", `,"cache_creation":null,"cache_creation_input_tokens":0`, `,"cache_creation":null,"output_tokens_details":null`, true},
		{"null-to-zero-details", `,"cache_creation":null,"output_tokens_details":null`, `,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens_details":{"thinking_tokens":0}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raws := executionSpeedFrames(`"fast"`)
			raws[0] = strings.Replace(raws[0], `,"speed":"fast"`, fixture.start, 1)
			raws[4] = strings.Replace(raws[4], `"output_tokens":2`, `"output_tokens":2`+fixture.delta, 1)
			frames, response := decodePackageStream(t, compiled, raws, p.Scope{})
			if response.Usage.Input.Count != 10 || response.Usage.Output.Count != 2 || (response.Usage.CacheCreation != nil) != fixture.hasCreation {
				t.Fatal("null usage observation changed the cumulative counter lifecycle", response.Usage)
			}
			if fixture.hasCreation && response.Usage.CacheCreation.Count != 0 {
				t.Fatal("observed/inferred zero was replaced by missing or another value", response.Usage)
			}
			if replayed := encodePackageStream(t, compiled, frames, p.Scope{}, true); !slices.Equal(replayed, raws) {
				t.Fatal("native replay lost nullable usage spelling or order", replayed)
			}
		})
	}
}
