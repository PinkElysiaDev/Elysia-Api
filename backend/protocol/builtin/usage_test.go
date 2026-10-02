package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestUsageDetailsPreserveExtensionsAndRejectUnsupportedCounters(t *testing.T) {
	compiled := testCompiled(t, Chat)
	body := `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0,"vendor":{"long":9007199254740993}},"billing":null}}`
	response, err := compiled.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	response.Model = p.StringValue("renamed")
	encoded, err := compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := testValue(t, string(encoded)).ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := fields["usage"].ReadObject()
	details, _ := usage["prompt_tokens_details"].ReadObject()
	sameJSON(t, details["vendor"].Bytes(), `{"long":9007199254740993}`)
	if !usage["billing"].IsNull() || response.Usage.CacheRead == nil || response.Usage.CacheRead.Count != 0 {
		t.Fatal("missing/null/zero changed", response.Usage)
	}
	if _, err := testCompiled(t, Responses).EncodeResponse(t.Context(), response, p.EvaluationContext{}); err == nil {
		t.Fatal("unknown billing extension crossed wire families")
	}
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"choices":[],"usage":{"prompt_tokens":10,"prompt_tokens_details":{"vendor":false}}}`), p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil || len(frame.Events) != 1 || frame.Events[0].Unmapped == nil {
		t.Fatal("usage-tail extension was discarded", frame, err)
	}
	usageWithTTL := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{"ephemeral_1h_input_tokens": {Count: 2, Origin: p.ObservedCount}}}
	if _, err := (module{name: Chat, family: "openai_chat"}).encodeUsage(usageWithTTL); err == nil {
		t.Fatal("TTL detail was silently dropped")
	}
}

func TestUsageMissingBucketAndReasoningNormalization(t *testing.T) {
	anthropic := module{name: Anthropic, family: "claude"}
	usage, err := anthropic.decodeUsage(testValue(t, `{"cache_creation":{"ephemeral_1h_input_tokens":9}}`))
	if err != nil {
		t.Fatal(err)
	}
	if usage.CacheCreation != nil || usage.Total != nil {
		t.Fatal("absent creation bucket became an explicit zero", usage)
	}
	gemini := module{name: Gemini, family: "gemini"}
	usage, err = gemini.decodeUsage(testValue(t, `{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Output.Count != 5 || usage.Details["output.reasoning_tokens"].Count != 3 {
		t.Fatal(usage)
	}
	encoded, err := gemini.encodeUsage(usage)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, encoded.Bytes(), `{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":7,"totalTokenCount":15}`)
}
