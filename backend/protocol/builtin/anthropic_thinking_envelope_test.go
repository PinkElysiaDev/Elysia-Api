package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicUnsignedThinkingEnvelope(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"visible thought","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	projected, err := c.Response(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"signature":""`) {
		t.Fatal("required signature field missing", string(wire))
	}
	back, err := to.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Content[0].Children[0].Resources) != 0 {
		t.Fatal("empty field minted provider state")
	}
	if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(string(wire), `"signature":"",`, "", 1)
	if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, missing)); err == nil || !strings.Contains(err.Error(), "/content/0/signature") {
		t.Fatal("malformed final thinking block accepted", err)
	}
	bad := strings.Replace(string(wire), `"signature":""`, `"signature":42`, 1)
	if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, bad)); err == nil {
		t.Fatal("invalid signature type accepted")
	}
	native, err := to.EncodeResponse(t.Context(), back, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, native, string(wire))
}
