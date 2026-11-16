package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestCanonicalContentMatchesCodec(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, raw := range []string{`""`, `"hello"`, `"` + strings.Repeat("x", 256<<10) + `"`, ` "\u0068ello" `, `"<>&"`, `"\u2028\u2029"`, `"😀"`, `"a\\b\n\t\""`, "\"\xff\""} {
		value := testValue(t, raw)
		var decoded string
		if err := value.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":`+raw+`}]}`), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		if len(request.Content) != 1 || len(request.Content[0].Children) != 1 || request.Content[0].Children[0].Kind != p.TextNode {
			t.Fatal("canonical content lost its message/text shape")
		}
		var text string
		if err := request.Content[0].Children[0].Payload.Decode(&text); err != nil || text != decoded || value.JSONString() != raw {
			t.Fatal("canonical text or source bytes changed", err)
		}
	}
}

func TestChatSelectedMembersPreserveDecodedDuplicateKeys(t *testing.T) {
	compiled := testCompiled(t, Chat)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	value := testValue(t, `{"choices":[{"index":1,"\u0069ndex":0,"delta":{"content":"discard","co\u006etent":"kept"}}]}`)
	frame, err := compiled.DecodeFrame(t.Context(), value, options)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range frame.Events {
		if event.Type == p.ItemDelta {
			if event.Delta != p.StringValue("kept") {
				t.Fatal("duplicate field did not use last decoded key")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("text delta missing")
	}
}
