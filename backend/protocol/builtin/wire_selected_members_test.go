package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestWireSelectedMembersUseLastDecodedKey(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct{ name, choice, issue string }{
		{"escaped-role", `{"index":0,"delta":{"role":"user","ro\u006ce":"assistant"},"finish_reason":"stop"}`, ""},
		{"invalid-last-role", `{"index":0,"delta":{"role":"assistant","ro\u006ce":"user"},"finish_reason":"stop"}`, "/delta/role"},
		{"escaped-delta", `{"index":0,"delta":null,"de\u006cta":{"role":"assistant"},"finish_reason":"stop"}`, ""},
		{"null-last-delta", `{"index":0,"delta":{"role":"assistant"},"de\u006cta":null,"finish_reason":"stop"}`, "/delta"},
		{"escaped-tool-index", `{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"ind\u0065x":0}]},"finish_reason":"tool_calls"}`, ""},
		{"sparse-last-tool-index", `{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"ind\u0065x":1}]},"finish_reason":"tool_calls"}`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			frame := testValue(t, `{"id":"r","object":"chat.completion.chunk","model":"m","created":1,"choices":[`+fixture.choice+`]}`)
			err := compiled.ValidateWireOutput(p.EncodeEvent, frame)
			if fixture.issue == "" && err != nil || fixture.issue != "" && (err == nil || !strings.Contains(err.Error(), fixture.issue)) {
				t.Fatal("escaped member did not select its last decoded key", err)
			}
		})
	}
}
