package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatAbsentThinkingDoesNotBecomeResponsesReasoning(t *testing.T) {
	chat, responses := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Responses)
	for _, value := range []string{`null`, `""`, `"visible thought"`} {
		body := `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK","reasoning_content":` + value + `},"finish_reason":"stop"}]}`
		r, err := chat.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := chat.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(responses, chat))
		r, err = c.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err == nil {
			wire, err = responses.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		}
		if value == `"visible thought"` {
			if err != nil || !strings.Contains(string(wire), `"type":"reasoning_text"`) || strings.Contains(string(wire), `"type":"summary_text"`) {
				t.Fatal("visible thought must use content, not summary", string(wire), err)
			}
			continue
		}
		if err != nil || strings.Contains(string(wire), `"type":"reasoning"`) {
			t.Fatal(string(wire), err)
		}
	}
}
