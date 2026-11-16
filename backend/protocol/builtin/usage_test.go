package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func usageResponseBody(name, usage string) string {
	switch name {
	case Chat, "openai-chat-completions":
		return `{"id":"r","model":"m","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":` + usage + `}`
	case Responses, "openai-responses":
		return `{"id":"r","model":"m","object":"response","status":"completed","output":[],"usage":` + usage + `}`
	case Anthropic, "anthropic-messages":
		return `{"id":"r","model":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":` + usage + `}`
	case Gemini, "google-generate-content":
		return `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":` + usage + `}`
	default:
		panic(fmt.Sprintf("missing usage fixture for %s", name))
	}
}

func decodeUsageResponse(t *testing.T, compiled *p.Compiled, raw string) (*p.Usage, error) {
	t.Helper()
	response, err := compiled.DecodeResponse(t.Context(), []byte(usageResponseBody(compiled.Identity().DefinitionID, raw)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		return nil, err
	}
	return response.Usage, nil
}

func encodeUsageResponse(t *testing.T, compiled *p.Compiled, usage *p.Usage) (p.Value, error) {
	t.Helper()
	response, err := compiled.DecodeResponse(t.Context(), []byte(usageResponseBody(compiled.Identity().DefinitionID, `{}`)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		return p.Value{}, err
	}
	response.Native, response.Usage = nil, usage
	wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		return p.Value{}, err
	}
	fields, _ := wire.ReadObject()
	if compiled.Identity().DefinitionID == "google-generate-content" {
		return fields["usageMetadata"], nil
	}
	return fields["usage"], nil
}

func usageResponse(t *testing.T, usage *p.Usage) *p.Response {
	t.Helper()
	return &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"),
		Attributes: p.Object{"created_at": testValue(t, "1")}, Usage: usage,
		Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}}}
}

func TestUsageDetailsPreserveExtensionsAndRejectUnsupportedCounters(t *testing.T) {
	compiled := testCompiled(t, Chat)
	body := `{"id":"r","model":"m","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0,"vendor":{"long":9007199254740993}},"billing":null},"vendor":{"enabled":false}}`
	response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	response.Model = p.StringValue("renamed")
	response.Native = nil
	response.Usage.Input.Count = 11
	encoded, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := encoded.ReadObject()
	usage, _ := fields["usage"].ReadObject()
	details, _ := usage["prompt_tokens_details"].ReadObject()
	sameJSON(t, details["vendor"].Bytes(), `{"long":9007199254740993}`)
	sameJSON(t, fields["vendor"].Bytes(), `{"enabled":false}`)
	if !fields["model"].IsString("renamed") || usage["prompt_tokens"].JSONString() != "11" {
		t.Fatal("native remainder replaced current core fields", encoded)
	}
	if !usage["billing"].IsNull() || response.Usage.CacheRead == nil || response.Usage.CacheRead.Count != 0 {
		t.Fatal("missing/null/zero changed", response.Usage)
	}
	for _, target := range []string{Responses, Anthropic, Gemini} {
		if _, err := testCompiled(t, target).EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
			t.Fatal("unknown response extension crossed wire families", target)
		}
	}
	owned := response.Attributes["wire:openai_chat"]
	response.Attributes["wire:openai_chat"] = testValue(t, `{"model":"collision"}`)
	if _, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("response extension overrode the current model")
	}
	response.Attributes["wire:openai_chat"] = owned
	state := p.NewEvaluationState()
	defer state.Release()
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"choices":[],"usage":{"prompt_tokens":10,"prompt_tokens_details":{"vendor":false}}}`), fixtureOptions(p.EvaluationContext{State: state}))
	if err != nil || len(frame.Events) != 1 || frame.Events[0].Unmapped == nil {
		t.Fatal("usage-tail extension was discarded", frame, err)
	}
	response = usageResponse(t, &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{"ephemeral_1h_input_tokens": {Count: 2, Origin: p.ObservedCount}}})
	if _, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("encoder accepted unprojected TTL")
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, testCompiled(t, Anthropic)))
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	response, err = conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, testCompiled(t, Anthropic).Identity()), sink)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil || !strings.Contains(encoded.JSONString(), `"cache_write_tokens":2`) || strings.Contains(encoded.JSONString(), "ephemeral") {
		t.Fatal("TTL detail leaked or creation count was lost", encoded, err)
	}
	requireUsageOmission(t, sink, "/usage/details/ephemeral_1h_input_tokens")
}

func requireUsageOmission(t *testing.T, sink *p.DiagnosticSink, path string) {
	t.Helper()
	count := 0
	for _, issue := range sink.Issues() {
		if issue.Path == path && issue.Severity == p.SeverityWarning {
			count++
		}
	}
	if count != 1 {
		t.Fatal("usage omission must be diagnosed once", path, sink.Issues())
	}
}

func TestUsageGeminiCreationTotalIsProjectedAndDiagnosed(t *testing.T) {
	target, source := testCompiled(t, Gemini), testCompiled(t, Anthropic)
	response := usageResponse(t, &p.Usage{Input: &p.Counter{Count: 27, Origin: p.ObservedCount}, Output: &p.Counter{Count: 2, Origin: p.ObservedCount}, CacheRead: &p.Counter{Count: 15, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 7, Origin: p.ObservedCount}})
	delete(response.Attributes, "created_at")
	if _, err := target.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("encoder accepted unprojected creation count")
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		t.Fatal(err)
	}
	route := fixtureConversionContext(t, conversion, p.ConversionResponse, source.Identity())
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Response(t.Context(), response, route, sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := target.EncodeResponseValue(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := wire.ReadObject()
	sameJSON(t, fields["usageMetadata"].Bytes(), `{"promptTokenCount":27,"candidatesTokenCount":2,"cachedContentTokenCount":15}`)
	requireUsageOmission(t, sink, "/usage/cacheCreation")
	if _, err := conversion.Response(t.Context(), response, route, nil); err != nil {
		t.Fatal("nil diagnostic sink changed the projection", err)
	}
}

func TestUsageMissingBucketAndReasoningNormalization(t *testing.T) {
	anthropic := testCompiled(t, Anthropic)
	response, err := anthropic.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"cache_creation":{"ephemeral_1h_input_tokens":9}}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.CacheCreation != nil || response.Usage.Total != nil {
		t.Fatal("absent creation bucket became an explicit zero", response.Usage)
	}
	gemini := testCompiled(t, Gemini)
	response, err = gemini.DecodeResponse(t.Context(), []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":7}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.Output.Count != 5 || response.Usage.Details["output.reasoning_tokens"].Count != 3 {
		t.Fatal("reasoning output was not counted once", response.Usage)
	}
	response.Native = nil
	wire, err := gemini.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := wire.ReadObject()
	sameJSON(t, fields["usageMetadata"].Bytes(), `{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":7,"totalTokenCount":15}`)
}
