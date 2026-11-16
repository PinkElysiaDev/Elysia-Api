package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestChatToolHistoryNoTextEnvelope(t *testing.T) {
	chat, gemini := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Gemini)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(chat, gemini))
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`null`, `""`, `"before"`} {
		body := `{"model":"m","messages":[{"role":"assistant","content":` + content + `,"tool_calls":[{"id":"c","type":"function","function":{"name":"echo","arguments":"{}"}}]}]}`
		r, err := chat.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := chat.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		projected, err := conversion.Request(t.Context(), r, p.ConversionContext{Source: chat.Identity(), Target: gemini.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err = gemini.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), `"text":`) != (content == `"before"`) {
			t.Fatal("no-text placeholder became Gemini part", string(wire))
		}
	}
}
