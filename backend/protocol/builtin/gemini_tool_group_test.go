package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiFunctionGroupCrossProtocol(t *testing.T) {
	source := compileBuiltinPackage(t, "google-generate-content")
	request, err := source.DecodeRequest(t.Context(), []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Kind != p.FunctionTool || request.Tools[0].Name != p.StringValue("lookup") {
		t.Fatal("function group did not decode to one core function", request.Tools)
	}
	for _, name := range []string{Chat, Responses, Anthropic} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, packageName(name))
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
			if err != nil {
				t.Fatal(err)
			}
			projected, err := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, request.Source), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})); err != nil {
				t.Fatal("core function could not reach the target", err)
			}
		})
	}
}

func TestGeminiToolResponseStatusProjection(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "google-generate-content")
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("response"), Model: p.StringValue("m"), Status: p.StringValue("completed"),
		Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, Status: p.StringValue("completed"), CallID: p.StringValue("call"), Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}}}}
	original := response.Content[0].Children[0]
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			policy := declaredRulePolicy(t, target, source, "response-shape", "compatible")
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				policy.Rules[0].Enabled = false
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Response(t.Context(), response, route, sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if mode == "disabled" {
				if !isUnsupported(err) {
					t.Fatal("unresolved tool completion reached the wire", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := target.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			_, call := toolResultStatusNodes(decoded.Content)
			if call == nil || call.CallID != original.CallID || call.Input.Value != original.Input.Value {
				t.Fatal("completion normalization altered tool identity or arguments", string(wire))
			}
			issues := sink.Issues()
			if len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].Path != "/content/0/children/0/status" || response.Content[0].Children[0].Status != original.Status {
				t.Fatal("completion normalization lost attribution or changed its source", issues)
			}
		})
	}
	conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "response-shape", "compatible"))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"incomplete", "in_progress", "failed"} {
		input := *response
		input.Content = append([]p.Node(nil), response.Content...)
		input.Content[0].Children = append([]p.Node(nil), response.Content[0].Children...)
		input.Content[0].Children[0].Status = p.StringValue(status)
		if _, err := conversion.Response(t.Context(), &input, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil); err == nil {
			t.Fatal("conflicting tool completion was discarded", status)
		}
	}
}

func TestGeminiResponsesFunctionProfile(t *testing.T) {
	report := p.VerifyBindingCombination(t.Context(), compileBuiltinPackage(t, "google-generate-content"), compileBuiltinPackage(t, "openai-responses"), p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.FunctionToolsCapability: true})
	if !report.Passed {
		t.Fatal("function profile lost its independent response roundtrip", report.Issues)
	}
}
