package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicThinkingUsageMapping(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, detail := range []string{`null`, `{}`, `{"thinking_tokens":0}`, `{"thinking_tokens":3}`} {
		u, err := decodeUsageResponse(t, compiled, fmt.Sprintf(`{"input_tokens":5,"output_tokens":7,"output_tokens_details":%s}`, detail))
		if err != nil || u.Output.Count != 7 || u.Total.Count != 12 {
			t.Fatalf("thinking counted twice: %+v %v", u, err)
		}
		wire, err := encodeUsageResponse(t, compiled, u)
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := wire.ReadObject()
		if detail != `null` && detail != `{}` && fields["output_tokens_details"] != testValue(t, detail) {
			t.Fatal("observed zero or nonzero detail lost", string(wire.Bytes()))
		}
	}
	for _, detail := range []string{`42`, `{"thinking_tokens":null}`, `{"thinking_tokens":-1}`, `{"thinking_tokens":"3"}`} {
		if _, err := decodeUsageResponse(t, compiled, `{"output_tokens_details":`+detail+`}`); err == nil {
			t.Fatal("invalid thinking counter accepted", detail)
		}
	}
	response, err := compiled.DecodeResponse(t.Context(), []byte(usageResponseBody(Anthropic, `{"output_tokens_details":{"thinking_tokens":0,"vendor":1}}`)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	wire, err := compiled.EncodeResponseValue(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := wire.ReadObject()
	usage, _ := fields["usage"].ReadObject()
	sameJSON(t, usage["output_tokens_details"].Bytes(), `{"thinking_tokens":0,"vendor":1}`)
}

// @anthropic-ai/sdk 0.132.1 declares output_tokens_details.thinking_tokens
// as a subset of output_tokens. It is not an extra output subtotal.

func TestGeminiThinkingUsageIsLosslessToCurrentAnthropic(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Anthropic)
	response, err := from.DecodeResponse(t.Context(), []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":8}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(to, from)
	policy.Mode = "strict"
	c, _ := p.ResolveConversion(policy)
	sink := &p.DiagnosticSink{}
	projected, err := c.Response(t.Context(), response, fixtureConversionContext(t, c, p.ConversionResponse, response.Source), sink)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range sink.Issues() {
		if issue.Fidelity != "preserved" {
			t.Fatal("lossless usage conversion degraded", issue)
		}
	}
	fields, _ := testValue(t, string(encoded)).ReadObject()
	usage, _ := fields["usage"].ReadObject()
	if usage["output_tokens"] != testValue(t, "5") || usage["output_tokens_details"] != testValue(t, `{"thinking_tokens":3}`) {
		t.Fatalf("unnecessary loss: %s %+v", encoded, sink.Issues())
	}
}
