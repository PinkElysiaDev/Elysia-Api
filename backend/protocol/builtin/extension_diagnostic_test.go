package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestForeignWireExtensionNamesTheUnsupportedField(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{Scope: p.Scope{Model: "m"}}
	for _, tc := range []struct{ field, path string }{
		{`"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"}]`, "/wire:gemini/safetySettings"},
		{`"systemInstruction":{"role":"user","parts":[{"text":"prefix"}],"vendor":{"state":"private-value"}}`, "/wire:gemini/systemInstruction/vendor/state"},
		{`"z":false,"a/b~c":{"token":"private-value"}`, "/wire:gemini/a~1b~0c/token"},
		{`"vendor":{}`, "/wire:gemini/vendor"},
		{`"vendor":null`, "/wire:gemini/vendor"},
	} {
		body := `{"contents":[{"role":"user","parts":[{"text":"question"}]}],` + tc.field + `}`
		r, err := from.DecodeRequest(t.Context(), []byte(body), options)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			_, err := to.EncodeRequest(t.Context(), r, options)
			requireContextIssue(t, err, p.UnsupportedCapability, tc.path)
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("extension value exposed in diagnostic", err)
			}
		}
		wire, err := from.EncodeRequest(t.Context(), r, options)
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
	}
}
