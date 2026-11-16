package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesOutputLayoutPreservesOrderedCoreItems(t *testing.T) {
	target := compileBuiltinPackage(t, "openai-responses")
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Content: []p.Node{{
		Kind: p.MessageNode, ID: p.StringValue("source-message"), Role: p.StringValue("assistant"), Children: []p.Node{
			{Kind: p.TextNode, Payload: p.StringValue("before")},
			{Kind: p.ReasoningNode, Payload: p.StringValue("thought")},
			{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}},
			{Kind: p.TextNode, Payload: p.StringValue("after")},
		},
	}}}
	before, _ := p.EncodeValue(response)
	options := fixtureOptions(p.EvaluationContext{})
	route := p.ConversionContext{Target: target.Identity(), Transport: p.HTTPJSON}.WithOperation(options)
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			policy := p.DefaultConversionPolicy(target, target)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for index := range policy.Rules {
					if policy.Rules[index].ID == "response-output-layout" {
						policy.Rules[index].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Response(t.Context(), response, route, sink)
			var wire p.Value
			if err == nil {
				wire, err = target.EncodeResponseValue(t.Context(), projected, options)
			}
			if mode != "compatible" {
				if err == nil {
					t.Fatal("undeclared layout change reached output", mode, wire)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeResponseValue(t.Context(), wire, options)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range []*p.Response{projected, decoded} {
				if len(result.Content) != 4 || result.Content[0].Kind != p.MessageNode || result.Content[1].Kind != p.ReasoningNode || result.Content[2].Kind != p.ToolCallNode || result.Content[3].Kind != p.MessageNode {
					t.Fatal("target core and wire disagree on item boundaries", result.Content, wire)
				}
				if !result.Content[0].Children[0].Payload.IsString("before") || !result.Content[1].Payload.IsString("thought") || !result.Content[2].CallID.IsString("call") || !result.Content[3].Children[0].Payload.IsString("after") {
					t.Fatal("layout changed content order or tool association", result.Content)
				}
				sameJSON(t, result.Content[2].Input.Value.Bytes(), `{"n":9007199254740993}`)
				if result.Content[0].ID == result.Content[3].ID {
					t.Fatal("split message segments reused one wire identity")
				}
			}
			hasLayoutDiagnostic := false
			for _, issue := range sink.Issues() {
				hasLayoutDiagnostic = hasLayoutDiagnostic || issue.RuleID == "response-output-layout" && issue.Code == p.ConversionDegraded
			}
			if !hasLayoutDiagnostic {
				t.Fatal("message boundary change had no diagnostic", sink.Issues())
			}
		})
	}
	after, _ := p.EncodeValue(response)
	if before != after {
		t.Fatal("target layout mutated caller-owned content")
	}
}
