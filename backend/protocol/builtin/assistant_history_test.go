package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesAssistantHistoryUsesOutputText(t *testing.T) {
	to := shippedProjectionProtocol(t, Responses)
	for _, source := range []string{Chat, Anthropic, Gemini} {
		from := shippedProjectionProtocol(t, source)
		wire := `{"model":"m","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"OK"},{"role":"user","content":"continue"}],"max_tokens":64}`
		if source == Gemini {
			wire = `{"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":[{"text":"OK"}]},{"role":"user","parts":[{"text":"continue"}]}]}`
		}
		request, err := from.DecodeRequest(t.Context(), []byte(wire), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
		request, err = c.Request(t.Context(), request, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := to.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		root, _ := testValue(t, string(encoded)).ReadObject()
		items, _ := readArray(root["input"])
		for i, item := range items {
			message, _ := item.ReadObject()
			parts, _ := readArray(message["content"])
			part, _ := parts[0].ReadObject()
			want := "input_text"
			if i == 1 {
				want = "output_text"
			}
			if part["type"] != p.StringValue(want) {
				t.Fatalf("%s history role misencoded: %s", source, encoded)
			}
		}
		if err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(encoded))); err != nil {
			t.Fatal(err)
		}
	}
	bad := testValue(t, `{"model":"m","input":[{"role":"assistant","content":[{"type":"input_text","text":"OK"}]}]}`)
	if err := to.ValidateWireOutput(p.EncodeRequest, bad); err == nil {
		t.Fatal("invalid assistant part from after mapping passed final validation")
	}
}
