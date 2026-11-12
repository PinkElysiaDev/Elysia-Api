package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestWireSelectedMembersUseLastDecodedKey(t *testing.T) {
	for _, tt := range []struct{ name, choice, issue string }{
		{"escaped-role", `{"index":0,"delta":{"role":"user","ro\u006ce":"assistant"},"finish_reason":"stop"}`, ""},
		{"invalid-last-role", `{"index":0,"delta":{"role":"assistant","ro\u006ce":"user"},"finish_reason":"stop"}`, "/delta/role"},
		{"escaped-delta", `{"index":0,"delta":null,"de\u006cta":{"role":"assistant"},"finish_reason":"stop"}`, ""},
		{"null-last-delta", `{"index":0,"delta":{"role":"assistant"},"de\u006cta":null,"finish_reason":"stop"}`, "expected JSON object"},
		{"escaped-tool-index", `{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":1,"ind\u0065x":0}]},"finish_reason":"tool_calls"}`, ""},
		{"gap-last-tool-index", `{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"ind\u0065x":1}]},"finish_reason":"tool_calls"}`, "gaps"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stream := streamModule{module: module{name: Chat}, limits: p.DefaultLimits()}
			frame := testValue(t, `{"choices":[`+tt.choice+`]}`)
			err := stream.ValidateWireEvent(frame)
			if tt.issue == "" && err != nil || tt.issue != "" && (err == nil || !strings.Contains(err.Error(), tt.issue)) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestWireHistorySelectedMembersPreserveAssociations(t *testing.T) {
	for _, tt := range []struct{ name, messages, issue string }{
		{"escaped-role-and-id", `[{"role":"user","ro\u006ce":"assistant","tool_calls":[{"id":"wrong","i\u0064":"c"}]},{"role":"tool","tool_call_id":"wrong","tool_call_i\u0064":"c"}]`, ""},
		{"invalid-last-id", `[{"role":"assistant","tool_calls":[{"id":"c"}]},{"role":"tool","tool_call_id":"c","tool_call_i\u0064":"wrong"}]`, "/tool_call_id"},
		{"null-last-calls", `[{"role":"assistant","tool_calls":[{"id":"c"}],"tool_c\u0061lls":null}]`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := readArray(testValue(t, tt.messages))
			if err != nil {
				t.Fatal(err)
			}
			err = validateChatToolSequence(messages)
			if tt.issue == "" && err != nil || tt.issue != "" && (err == nil || !strings.Contains(err.Error(), tt.issue)) {
				t.Fatalf("unexpected association result: %v", err)
			}
		})
	}
}
