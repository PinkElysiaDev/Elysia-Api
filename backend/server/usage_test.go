package server

import (
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestNativeUsageUsesPinnedProtocolAndPreservesPresence(t *testing.T) {
	for _, fixture := range []struct {
		name, id, body             string
		input, output, total, read *int
	}{
		{"absent", "chat-completions-api", `{"choices":[]}`, nil, nil, nil, nil},
		{"null", "chat-completions-api", `{"choices":[],"usage":null}`, nil, nil, nil, nil},
		{"zero", "chat-completions-api", `{"choices":[],"usage":{"total_tokens":0}}`, nil, nil, intPtr(0), nil},
		{"chat", "chat-completions-api", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":4}}}`, intPtr(10), intPtr(20), intPtr(30), intPtr(4)},
		{"responses", "responses-api", `{"output":[],"status":"completed","usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":25}}}`, intPtr(100), intPtr(50), intPtr(150), intPtr(25)},
		{"anthropic", "anthropic-api", `{"content":[],"usage":{"input_tokens":100,"output_tokens":200,"cache_read_input_tokens":30,"cache_creation_input_tokens":50}}`, intPtr(180), intPtr(200), intPtr(380), intPtr(30)},
		// Gemini's prompt count includes its tool prompt detail. Do not add
		// the detail again; reasoning is separate from candidates output.
		{"gemini", "gemini-api", `{"candidates":[],"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":5,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":37,"cachedContentTokenCount":3}}`, intPtr(10), intPtr(27), intPtr(37), intPtr(3)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileFixtureDefinition(t, presetDefinition(t, fixture.id))
			response, err := compiled.DecodeResponse(t.Context(), []byte(fixture.body), protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			record := &usageRecord{}
			updateRecordProtocolUsage(record, response.Usage)
			got := []*int{record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.TotalTokens, record.Usage.CacheHitTokens}
			for i, want := range []*int{fixture.input, fixture.output, fixture.total, fixture.read} {
				if (got[i] == nil) != (want == nil) || want != nil && *got[i] != *want {
					t.Fatal("presence or normalized usage lost", record.Usage)
				}
			}
		})
	}
}

func TestRequestEstimateDoesNotBecomeActualUsage(t *testing.T) {
	record := &usageRecord{Usage: usageTokenUsage{Estimated: true, EstimatedTokens: 1000010}}
	updateRecordProtocolUsage(record, nil)
	if record.Usage.InputTokens != nil || record.Usage.OutputTokens != nil || record.Usage.TotalTokens != nil || !record.Usage.Estimated {
		t.Fatal("estimate became observed usage", record.Usage)
	}
}

func TestUsageRequestIDUniqueForSameNanosecond(t *testing.T) {
	now := time.Now()
	if usageRequestID(now) == usageRequestID(now) {
		t.Fatal("usage IDs must carry a random suffix")
	}
}
