package builtin

import (
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestCanonicalContentMatchesCodec(t *testing.T) {
	for _, raw := range []string{
		`""`, `"hello"`, `"` + strings.Repeat("x", 256<<10) + `"`,
		` "\u0068ello" `, `"<>&"`, `"\u2028\u2029"`, `"😀"`,
		`"a\\b\n\t\""`, "\"\xff\"",
	} {
		value := testValue(t, raw)
		var decoded string
		if err := value.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		want := []p.Node{{Kind: p.TextNode, Payload: p.StringValue(decoded)}}
		got, err := (module{name: Chat}).decodeContent(value, "/content", p.DecodeRequest, p.EvaluationContext{}, nil)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: %v", raw, err)
		}
		if value.JSONString() != raw {
			t.Fatal("input modified")
		}
	}
}

func TestChatSelectedMembersPreserveDecodedDuplicateKeys(t *testing.T) {
	compiled := testCompiled(t, Chat)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
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
