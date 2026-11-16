package builtin

import (
	"errors"

	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// This candidate has no production registration or activation samples. The old
// codec is only the test oracle; the package compiler has no installed modules.

func TestChatPackageResponseGroups(t *testing.T) {
	draft := compileBuiltinPackage(t, "openai-chat")
	policy := declaredRulePolicy(t, draft, draft, "response-layout", "compatible")
	policy.Rules = append(policy.Rules, declaredRulePolicy(t, draft, draft, "response-text", "compatible").Rules...)
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	text := p.Node{Kind: p.TextNode, Payload: p.StringValue("text")}
	message := p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{text}}
	annotated := message
	annotated.ID = p.StringValue("independent")
	firstChoice, secondChoice := message, message
	firstChoice.Attributes = p.Object{"choiceIndex": testValue(t, `3`), "finishReason": p.StringValue("length")}
	secondChoice.Attributes = p.Object{"choiceIndex": testValue(t, `7`), "finishReason": p.StringValue("stop")}
	for _, fixture := range []struct {
		name       string
		content    []p.Node
		isRejected bool
		choices    string
	}{
		{"empty", nil, false, `[{"finish_reason":"stop","index":0,"message":{"role":"assistant"}}]`},
		{"standalone", []p.Node{text}, false, `[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"text"}}]`},
		{"messages", []p.Node{message, message}, false, `[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"texttext"}}]`},
		{"mixed", []p.Node{message, {Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}, false, `[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"text","tool_calls":[{"type":"function","id":"c","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}]}}]`},
		{"choices", []p.Node{firstChoice, secondChoice}, false, `[{"finish_reason":"length","index":3,"message":{"role":"assistant","content":"text"}},{"finish_reason":"stop","index":7,"message":{"role":"assistant","content":"text"}}]`},
		{"annotated-message", []p.Node{annotated, text}, true, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			response := &p.Response{SchemaVersion: draft.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Content: fixture.content, Attributes: p.Object{"finishReason": p.StringValue("stop"), "created_at": testValue(t, "1")}}
			projected, err := conversion.Response(t.Context(), response, p.ConversionContext{Source: draft.Identity(), Target: draft.Identity()}, nil)
			if fixture.isRejected {
				var failure *p.ConversionError
				if !errors.As(err, &failure) || failure.Issues[0].Code != p.UnsupportedCapability {
					t.Fatal("independent container identity was discarded", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := draft.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			value := testValue(t, string(wire))
			if err := draft.ValidateWireOutput(p.EncodeResponse, value); err != nil {
				t.Fatal(err)
			}
			fields, err := value.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, fields["choices"].Bytes(), fixture.choices)
		})
	}
}
