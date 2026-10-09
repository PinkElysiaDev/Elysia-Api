package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiSingletonRequiredToolChoiceIsNamedFunction(t *testing.T) {
	gemini := module{name: Gemini}
	choice, err := gemini.decodeChoice(testValue(t, `{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["audit_echo"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		adapter := module{name: codec}
		wire, err := adapter.encodeChoice(choice)
		if err != nil {
			t.Fatalf("%s: %v", codec, err)
		}
		decoded, err := adapter.decodeChoice(wire)
		if err != nil || decoded != choice {
			t.Fatalf("%s named choice changed: %s %v", codec, decoded.Bytes(), err)
		}
	}
	for _, raw := range []string{`{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["a","b"]}}`, `{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["a"]}}`} {
		choice, err := gemini.decodeChoice(testValue(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = (module{name: Chat}).encodeChoice(choice); err == nil {
			t.Fatal("unequal allowed-tool constraint discarded")
		}
	}
	if choice == (p.Value{}) {
		t.Fatal("choice missing")
	}
}
