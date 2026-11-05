package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageAnthropicAssistantGrouping(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "32")}, Content: []p.Node{
		{Kind: p.ReasoningNode, Payload: p.StringValue("first thought")},
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("first reply")}}},
		{Kind: p.ReasoningNode, Payload: p.StringValue("second thought")},
		{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("f"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, "{}")}},
		{Kind: p.ToolResultNode, CallID: p.StringValue("call"), Payload: p.StringValue("result")},
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("final reply")}}},
	}}
	before, _ := p.EncodeValue(request)
	policy := declaredRulePolicy(t, compiled, compiled, "assistant-history", "compatible")
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	sink := &p.DiagnosticSink{}
	result, err := conversion.Request(t.Context(), request, route, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 3 || len(result.Content[0].Children) != 4 || result.Content[1].Kind != p.ToolResultNode || !result.Content[2].Children[0].Payload.IsString("final reply") || len(sink.Issues()) != 4 {
		t.Fatal("assistant grouping changed tool barrier or turn order", result.Content, sink.Issues())
	}
	wire, err := compiled.EncodeRequest(t.Context(), result, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := compiled.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
	if err != nil || len(decoded.Content) != 3 || len(decoded.Content[0].Children) != 4 {
		t.Fatal("grouped history did not rebuild", string(wire), err)
	}
	blocks := decoded.Content[0].Children
	if !blocks[0].Payload.IsString("first thought") || !blocks[1].Payload.IsString("first reply") || !blocks[2].Payload.IsString("second thought") || blocks[3].Kind != p.ToolCallNode || !blocks[3].CallID.IsString("call") {
		t.Fatal("thinking and tool order changed", blocks)
	}
	policy.Mode = "strict"
	strict, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Request(t.Context(), request, route, nil); err == nil {
		t.Fatal("strict grouping hid root boundary loss")
	}
	policy.Mode, policy.Rules[0].Enabled = "compatible", false
	disabled, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	unresolved, err := disabled.Request(t.Context(), request, route, nil)
	if err == nil {
		_, err = compiled.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{})
	}
	if err == nil {
		t.Fatal("encoder repeated disabled assistant grouping")
	}
	after, _ := p.EncodeValue(request)
	if before != after {
		t.Fatal("grouping changed caller-owned history")
	}
}
