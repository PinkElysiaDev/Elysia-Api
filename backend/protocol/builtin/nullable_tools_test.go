package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatNullableToolCallsPreservePresence(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, suffix := range []string{``, `,"tool_calls":null`, `,"tool_calls":[]`} {
		t.Run(suffix, func(t *testing.T) {
			wire := `{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"OK"` + suffix + `},"finish_reason":"stop"}]}`
			response, err := compiled.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, encoded, wire)
			response.Content[0].Children[0].Payload = p.StringValue("updated")
			encoded, err = compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, encoded, strings.Replace(wire, `"OK"`, `"updated"`, 1))
			if _, err := testCompiled(t, Anthropic).EncodeResponse(t.Context(), response, p.EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := compiled.DecodeResponse(t.Context(), []byte(`{"choices":[{"message":{"role":"assistant","tool_calls":{}}}]}`), p.EvaluationContext{}); err == nil {
		t.Fatal("non-null invalid tool array accepted")
	}
}
