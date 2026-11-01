package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageResponseRootLayout(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("tool_calls")}, Content: []p.Node{
				{Kind: p.ReasoningNode, Payload: p.StringValue("first thought")},
				{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("first reply")}}},
				{Kind: p.ReasoningNode, Payload: p.StringValue("second thought")},
				{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("f"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, "{}")}},
			}}
			if name == "anthropic" {
				response.Usage = &p.Usage{Input: &p.Counter{Count: 1, Origin: "observed"}, Output: &p.Counter{Count: 2, Origin: "observed"}}
			}
			before, _ := p.EncodeValue(response)
			policy := declaredRulePolicy(t, target, source, "response-layout", "compatible")
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			result, err := conversion.Response(t.Context(), response, route, sink)
			if err != nil || len(result.Content) != 1 || len(result.Content[0].Children) != 4 || len(sink.Issues()) != 4 {
				t.Fatal("response roots lost their ordered grouping", err, sink.Issues())
			}
			wire, err := target.EncodeResponse(t.Context(), result, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
			if err != nil || len(decoded.Content) != 1 || len(decoded.Content[0].Children) != 4 {
				t.Fatal("grouped response did not rebuild", string(wire), err)
			}
			blocks := decoded.Content[0].Children
			if !blocks[0].Payload.IsString("first thought") || !blocks[1].Payload.IsString("first reply") || !blocks[2].Payload.IsString("second thought") || blocks[3].Kind != p.ToolCallNode || !blocks[3].CallID.IsString("call") {
				t.Fatal("response grouping changed thinking or tools", blocks)
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := strict.Response(t.Context(), response, route, nil); err == nil {
				t.Fatal("strict mode accepted root boundary loss")
			}
			policy.Mode, policy.Rules[0].Enabled = "compatible", false
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			unresolved, err := disabled.Response(t.Context(), response, route, nil)
			if err == nil {
				_, err = target.EncodeResponse(t.Context(), unresolved, p.EvaluationContext{})
			}
			if err == nil {
				t.Fatal("encoder repeated disabled response grouping")
			}
			response.Content[1].Attributes = p.Object{"choiceIndex": testValue(t, "1")}
			if _, err := conversion.Response(t.Context(), response, route, nil); err == nil {
				t.Fatal("independent candidate merged into an assistant turn")
			}
			response.Content[1].Attributes = nil
			after, _ := p.EncodeValue(response)
			if before != after {
				t.Fatal("response projection changed caller input")
			}
		})
	}
}

func TestPackageGeminiSingleCandidateBoundary(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	wire := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}},{"content":{"role":"model","parts":[{"text":"two"}]}}]}`)
	if _, err := compiled.DecodeResponse(t.Context(), wire, p.EvaluationContext{}); err == nil {
		t.Fatal("multiple source candidates were accepted")
	}
	if err := compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err == nil {
		t.Fatal("multiple final candidates bypassed the target contract")
	}
}
