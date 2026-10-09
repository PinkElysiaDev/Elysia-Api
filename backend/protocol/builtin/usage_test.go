package builtin

import (
	"strings"
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
	sink := &p.DiagnosticSink{}
	if _, err := (module{name: Chat}).encodeUsage(usageWithTTL, p.EvaluationContext{}); err == nil {
		t.Fatal("encoder must reject unprojected TTL")
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(testCompiled(t, Chat), testCompiled(t, Anthropic)))
	if err != nil {
		t.Fatal(err)
	}
	response, err = conversion.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usageWithTTL}, p.ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := (module{name: Chat, family: "openai_chat"}).encodeUsage(response.Usage, p.EvaluationContext{Diagnostics: sink})
	if err != nil {
		t.Fatal("cross-protocol TTL bucket must project away, not fail", err)
	}
	// The total creation counter survives; only the provider-specific TTL
	// breakdown is omitted, and the omission is recorded as a warning.
	projectedFields, err := projected.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	projectedDetails, _ := projectedFields["prompt_tokens_details"].ReadObject()
	if projectedDetails["cache_write_tokens"].IsZero() || strings.Contains(string(projected.Bytes()), "ephemeral") {
		t.Fatalf("TTL bucket leaked or total creation lost: %s", projected.Bytes())
	}
	issues := sink.Issues()
	if len(issues) != 1 || issues[0].Severity != p.SeverityWarning || issues[0].Path != "/usage/details/ephemeral_1h_input_tokens" {
		t.Fatalf("omission must be diagnosed once: %+v", issues)
	}
}

// Gemini reports cache reads only. The provider's creation total has no target
// field, so the projection is recorded rather than failing a response the
// client can otherwise consume.
func TestUsageGeminiCreationTotalIsProjectedAndDiagnosed(t *testing.T) {
	gemini := module{name: Gemini, family: "gemini"}
	usage := &p.Usage{
		Input:         &p.Counter{Count: 27, Origin: p.ObservedCount},
		Output:        &p.Counter{Count: 2, Origin: p.ObservedCount},
		CacheRead:     &p.Counter{Count: 15, Origin: p.ObservedCount},
		CacheCreation: &p.Counter{Count: 7, Origin: p.ObservedCount},
		Details:       map[string]p.Counter{},
	}
	sink := &p.DiagnosticSink{}
	if _, err := gemini.encodeUsage(usage, p.EvaluationContext{}); err == nil {
		t.Fatal("encoder must reject unprojected cache creation")
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(testCompiled(t, Gemini), testCompiled(t, Anthropic)))
	if err != nil {
		t.Fatal(err)
	}
	response, err := conversion.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := gemini.encodeUsage(response.Usage, p.EvaluationContext{Diagnostics: sink})
	if err != nil {
		t.Fatal("a creation total Gemini cannot express must project away, not fail", err)
	}
	// Total is absent here because this usage carries no total counter; the
	// creation total is projected away while the read count survives.
	sameJSON(t, encoded.Bytes(), `{"promptTokenCount":27,"candidatesTokenCount":2,"cachedContentTokenCount":15}`)
	issues := sink.Issues()
	if len(issues) != 1 || issues[0].Severity != p.SeverityWarning || issues[0].Path != "/usage/cacheCreation" {
		t.Fatalf("omission must be diagnosed once: %+v", issues)
	}
	// Diagnostic collection does not change the named projection.
	if _, err := conversion.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, nil); err != nil {
		t.Fatalf("nil diagnostics must not change the projection: %v", err)
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
	encoded, err := gemini.encodeUsage(usage, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, encoded.Bytes(), `{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":7,"totalTokenCount":15}`)
}
