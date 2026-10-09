package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestChatToolHistoryNoTextEnvelope(t *testing.T) {
	chat, gemini := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Gemini)
	for _, content := range []string{`null`, `""`, `"before"`} {
		body := `{"model":"m","messages":[{"role":"assistant","content":` + content + `,"tool_calls":[{"id":"c","type":"function","function":{"name":"echo","arguments":"{}"}}]}]}`
		r, err := chat.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := chat.EncodeRequest(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		wire, err = gemini.EncodeRequest(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), `"text":`) != (content == `"before"`) {
			t.Fatal("no-text placeholder became Gemini part", string(wire))
		}
	}
}
