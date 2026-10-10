package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestChatCompleteToolIndexesAreValidatedRedundancy(t *testing.T) {
	from := shippedProjectionProtocol(t, Chat)
	raw := `{"id":"r","created":1,"object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"echo","arguments":"{\"n\":9007199254740993}"}},{"index":1,"id":"call2","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	r, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	native, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, native, raw)
	for _, codec := range []string{Responses, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
		projected, err := c.Response(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		if err != nil {
			t.Fatal(codec, err)
		}
		for _, expected := range []string{"call1", "call2", "9007199254740993"} {
			if !strings.Contains(string(wire), expected) {
				t.Fatal("tool association/precision lost", codec)
			}
		}
	}
	for _, index := range []string{"1", "null", `"0"`, "-1", "0.5"} {
		bad := strings.Replace(raw, `"index":0,"id":"call1"`, `"index":`+index+`,"id":"call1"`, 1)
		if _, err := from.DecodeResponse(t.Context(), []byte(bad), p.EvaluationContext{}); err == nil || !strings.Contains(err.Error(), "/tool_calls/0/index") {
			t.Fatal("invalid positional index accepted", index, err)
		}
	}
	bad := strings.Replace(raw, `"index":0,"id":"call1"`, `"index":0,"vendor":true,"id":"call1"`, 1)
	r, err = from.DecodeResponse(t.Context(), []byte(bad), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	to := shippedProjectionProtocol(t, Anthropic)
	if _, err = to.EncodeResponse(t.Context(), r, p.EvaluationContext{}); err == nil {
		t.Fatal("unknown extension swallowed")
	}
}
