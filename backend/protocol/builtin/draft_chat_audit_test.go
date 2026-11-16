package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDraftChatCacheAccounting(t *testing.T) {
	expectedCounts := map[string][4]int64{
		"nested-hit-infers-input": {5, 1, 6, 2}, "input-infers-hit": {5, 1, 6, 2},
		"observed-counts-and-alias-order": {5, 1, 9, 2}, "creation-adjusts-miss": {5, 1, 6, 2}, "missing-miss": {5, 1, 6, 2},
	}
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct {
		name, usage string
		isRejected  bool
	}{
		{"nested-hit-infers-input", `{"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_miss_tokens":3,"completion_tokens":1}`, false},
		{"input-infers-hit", `{"prompt_tokens":5,"prompt_cache_miss_tokens":3,"completion_tokens":1}`, false},
		{"observed-counts-and-alias-order", `{"prompt_tokens":5,"completion_tokens":1,"total_tokens":9,"cache_read_input_tokens":1,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":3}`, false},
		{"creation-adjusts-miss", `{"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":3,"completion_tokens":1,"cache_creation_input_tokens":1,"prompt_tokens_details":{"cache_write_tokens":1}}`, false},
		{"missing-miss", `{"prompt_tokens":5,"completion_tokens":1,"prompt_cache_hit_tokens":2}`, false},
		{"null-miss", `{"prompt_tokens":5,"prompt_cache_miss_tokens":null}`, true},
		{"fractional-miss", `{"prompt_tokens":5,"prompt_cache_miss_tokens":1.5}`, true},
		{"negative-miss", `{"prompt_tokens":5,"prompt_cache_miss_tokens":-1}`, true},
		{"masked-null-counter", `{"cache_read_input_tokens":null,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_miss_tokens":3}`, true},
		{"conflicting-hit", `{"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":3,"prompt_cache_miss_tokens":3}`, true},
		{"contradictory-miss", `{"prompt_tokens":5,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":4}`, true},
		{"creation-exceeds-miss", `{"prompt_tokens":5,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":3,"cache_creation_input_tokens":4}`, true},
		{"conflicting-creation", `{"prompt_tokens":5,"prompt_cache_miss_tokens":3,"cache_creation_input_tokens":1,"prompt_tokens_details":{"cache_write_tokens":2}}`, true},
		{"input-overflow", `{"prompt_tokens_details":{"cached_tokens":9223372036854775807},"prompt_cache_miss_tokens":1}`, true},
		{"total-overflow", `{"prompt_cache_hit_tokens":9223372036854775806,"prompt_cache_miss_tokens":1,"completion_tokens":1}`, true},
		{"miss-without-input", `{"prompt_cache_miss_tokens":3,"completion_tokens":1}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			response, err := compiled.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","choices":[],"usage":`+fixture.usage+`}`), newDraftAuditOptions())
			if fixture.isRejected {
				requireDraftRejection(t, err, "")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := expectedCounts[fixture.name]
			usage := response.Usage
			if usage == nil || usage.Input == nil || usage.Output == nil || usage.Total == nil || usage.CacheRead == nil || usage.Input.Count != expected[0] || usage.Output.Count != expected[1] || usage.Total.Count != expected[2] || usage.CacheRead.Count != expected[3] {
				t.Fatal("cache inference changed", usage)
			}
			if fixture.name == "creation-adjusts-miss" && (usage.CacheCreation == nil || usage.CacheCreation.Count != 1) {
				t.Fatal("creation counter lost", usage)
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, err := compiled.DecodeResponse(t.Context(), wire, newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			if roundtrip.Usage.Input.Count != usage.Input.Count || roundtrip.Usage.Output.Count != usage.Output.Count || roundtrip.Usage.Total.Count != usage.Total.Count || roundtrip.Usage.CacheRead.Count != usage.CacheRead.Count {
				t.Fatal("native-free output changed counters", roundtrip.Usage)
			}
		})
	}
}

func TestDraftChatReasoningInputs(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct{ name, reasoning, effort string }{
		{"absent", "", ""},
		{"legacy", "", `"high"`},
		{"legacy-null", "", `null`},
		{"legacy-unknown-value", "", `"future"`},
		{"empty-reasoning", `{}`, `"high"`},
		{"null-reasoning", `null`, `"high"`},
		{"typed", `{"effort":"high","effortScope":"reasoning"}`, ""},
		{"typed-null", `{"effort":null}`, ""},
		{"equal-effort-conflict", `{"effort":"high","effortScope":"reasoning"}`, `"high"`},
		{"legacy-null-conflict", `{"effort":"high","effortScope":"reasoning"}`, `null`},
		{"typed-null-conflict", `{"effort":null}`, `null`},
		{"null-budget-before-conflict", `{"effort":"high","effortScope":"reasoning","budget":null}`, `"high"`},
		{"null-summary", `{"summary":null}`, `"high"`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{}}
			if fixture.reasoning != "" {
				if err := testValue(t, fixture.reasoning).Decode(&request.Reasoning); err != nil {
					t.Fatal(err)
				}
			}
			if fixture.effort != "" {
				request.Parameters["reasoning_effort"] = testValue(t, fixture.effort)
			}
			before, _ := p.EncodeValue(request)
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if fixture.effort != "" {
				path := "/parameters"
				switch fixture.name {
				case "equal-effort-conflict", "legacy-null-conflict", "typed-null-conflict":
					path = "/reasoning/effort"
				case "null-budget-before-conflict", "null-summary":
					path = "/reasoning"
				}
				requireDraftRejection(t, err, path)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				fields, _ := wire.ReadObject()
				if request.Reasoning == nil {
					if !fields["reasoning_effort"].IsZero() {
						t.Fatal("missing effort invented")
					}
				} else if fields["reasoning_effort"] != request.Reasoning.Effort {
					t.Fatal("canonical effort changed", wire)
				}
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("reasoning validation mutated input")
			}
		})
	}
}

func TestDraftChatCollectedUsage(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct {
		name, stream, rawOptions, expected string
		shouldCollect                      bool
	}{
		{"missing-options", `true`, "", `{"include_usage":true}`, true},
		{"preserve-extension", `true`, `{"include_usage":false,"future":{"n":9007199254740993,"empty":null}}`, `{"future":{"n":9007199254740993,"empty":null},"include_usage":true}`, true},
		{"null-options", `true`, `null`, `{"include_usage":true}`, true},
		{"scalar-options", `true`, `false`, `{"include_usage":true}`, true},
		{"array-options", `true`, `[]`, `{"include_usage":true}`, true},
		{"collection-disabled", `true`, `{"include_usage":false}`, `{"include_usage":false}`, false},
		{"non-streaming", `false`, `{"include_usage":false}`, `{"include_usage":false}`, true},
		{"null-stream", `null`, `null`, `null`, true},
		{"missing-stream", "", "", "", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{}, ClientOutput: &p.ClientOutput{CollectUsage: fixture.shouldCollect}}
			if fixture.stream != "" {
				request.Parameters["stream"] = testValue(t, fixture.stream)
			}
			if fixture.rawOptions != "" {
				request.ClientOutput.RawStreamOptions = testValue(t, fixture.rawOptions)
			}
			before, _ := p.EncodeValue(request)
			wire, err := compiled.EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := wire.ReadObject()
			if fields["stream_options"].JSONString() != fixture.expected {
				t.Fatal("collection options changed", wire, fixture.expected)
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("collection controls mutated")
			}
		})
	}
}
