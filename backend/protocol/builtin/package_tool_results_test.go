package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageDeclaredToolResults(t *testing.T) {
	packages := map[string]*p.Compiled{}
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		packages[name] = compileBuiltinPackage(t, name)
	}
	for _, fixture := range []struct {
		name, target, payload, status, want string
		hasLoss                             bool
	}{
		{"json-looking-text", "google-generate-content", `"{\"value\":1}"`, "", `{"result":"{\"value\":1}"}`, true},
		{"object", "google-generate-content", `{"value":1}`, "", `{"value":1}`, false},
		{"error", "google-generate-content", `{"message":"failed"}`, `{"isError":true}`, `{"message":"failed"}`, false},
		{"text-error", "google-generate-content", `"failed"`, `{"isError":true}`, `{"result":"failed"}`, true},
		{"reserved-success", "google-generate-content", `{"error":"ordinary data"}`, `{"isError":false}`, `{"error":"ordinary data"}`, false},
		{"object-chat", "openai-chat", `{"value":1}`, "", `"{\"value\":1}"`, true},
		{"object-responses", "openai-responses", `{"value":1}`, "", `"{\"value\":1}"`, true},
		{"object-anthropic", "anthropic", `{"value":1}`, "", `"{\"value\":1}"`, true},
		{"error-chat", "openai-chat", `"failed"`, `{"isError":true}`, `"[Tool error] failed"`, true},
		{"error-responses", "openai-responses", `"failed"`, `{"isError":true}`, `"[Tool error] failed"`, true},
		{"error-anthropic", "anthropic", `"failed"`, `{"isError":true}`, `"failed"`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, target := packages["google-generate-content"], packages[fixture.target]
			request := toolResultRequest(t, fixture.payload, fixture.status)
			original, _ := p.EncodeValue(request)
			policy := declaredRulePolicy(t, source, target, "tool-results", "compatible")
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			sink := &p.DiagnosticSink{}
			result, err := conversion.Request(t.Context(), request, route, sink)
			if err != nil {
				t.Fatal(err)
			}
			node := result.Content[1]
			want := testValue(t, fixture.want)
			if node.Payload != want || !node.CallID.IsString("call") || !node.Name.IsString("f") {
				t.Fatal("result data or association changed", node)
			}
			if fixture.target == "anthropic" || fixture.target == "google-generate-content" {
				if node.Status != request.Content[1].Status {
					t.Fatal("separate error flag was lost")
				}
			} else if !node.Status.IsZero() {
				t.Fatal("resolved status retained", node.Status)
			}
			hasLoss := false
			for _, issue := range sink.Issues() {
				if issue.RuleID != "tool-results" {
					t.Fatal("wrong rule attribution", issue)
				}
				hasLoss = hasLoss || issue.Fidelity == "lossy_compatible"
			}
			if hasLoss != fixture.hasLoss {
				t.Fatal("loss classification changed", sink.Issues())
			}
			wire, err := target.EncodeRequest(t.Context(), result, p.EvaluationContext{})
			if err != nil {
				t.Fatal("resolved result did not encode", err)
			}
			decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
			if err != nil {
				t.Fatal("invalid target history", string(wire), err)
			}
			if fixture.target == "google-generate-content" {
				observed := decoded.Content[1].Children[0]
				if observed.Payload != node.Payload || !node.Status.IsZero() && observed.Status != node.Status {
					t.Fatal("object envelope changed result or error status", observed, string(wire))
				}
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = strict.Request(t.Context(), request, route, nil); (err != nil) != fixture.hasLoss {
				t.Fatal("strict loss boundary changed", err)
			}
			after, _ := p.EncodeValue(request)
			if after != original {
				t.Fatal("projection changed caller input")
			}
		})
	}
}

func TestPackageToolResultTextBlocks(t *testing.T) {
	source := compileBuiltinPackage(t, "anthropic")
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, target, "tool-results", "compatible"))
			if err != nil {
				t.Fatal(err)
			}
			request := toolResultRequest(t, `"unused"`, "")
			request.Content[1].Payload = p.Value{}
			request.Content[1].Children = []p.Node{{Kind: p.TextNode, Payload: p.StringValue("one")}, {Kind: p.TextNode, Payload: p.StringValue("two")}}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			result, err := conversion.Request(t.Context(), request, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			if name == "openai-chat" || name == "google-generate-content" {
				want := p.StringValue("onetwo")
				if name == "google-generate-content" {
					want = testValue(t, `{"result":"onetwo"}`)
				}
				if result.Content[1].Payload != want || len(result.Content[1].Children) != 0 {
					t.Fatal("text block projection changed content", result.Content[1])
				}
			} else if len(result.Content[1].Children) != 2 {
				t.Fatal("supported block boundaries lost")
			}
			if _, err = target.EncodeRequest(t.Context(), result, p.EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
			request.Content[1].Children[0].Attributes = p.Object{"future": p.StringValue("must survive")}
			if name == "openai-chat" || name == "google-generate-content" {
				if _, err = conversion.Request(t.Context(), request, route, nil); err == nil {
					t.Fatal("flattening removed unknown block semantics")
				}
			}
		})
	}
}

func toolResultRequest(t *testing.T, payload, status string) *p.Request {
	t.Helper()
	result := p.Node{Kind: p.ToolResultNode, CallID: p.StringValue("call"), Name: p.StringValue("f"), Payload: testValue(t, payload)}
	if status != "" {
		result.Status = testValue(t, status)
	}
	return &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "100")}, Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("f"), Input: &p.ToolInput{Kind: "json", Value: testValue(t, `{}`)}}}}, result,
	}}
}

func TestPackageToolResultMediaAndDisabledRules(t *testing.T) {
	source := compileBuiltinPackage(t, "anthropic")
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			policy := declaredRulePolicy(t, source, target, "tool-results", "compatible")
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			request := toolResultRequest(t, `"unused"`, "")
			request.Content[1].Payload = p.Value{}
			request.Content[1].Children = []p.Node{{Kind: p.ImageNode, Payload: testValue(t, `{"url":"https://example.test/image.png","mime":"image/png"}`)}}
			result, err := conversion.Request(t.Context(), request, route, nil)
			if name == "google-generate-content" || name == "openai-chat" {
				if err == nil {
					t.Fatal("media was consumed as ordinary object content")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Content[1].Children) != 1 || result.Content[1].Children[0].Kind != p.ImageNode {
					t.Fatal("media result was flattened")
				}
				wire, err := target.EncodeRequest(t.Context(), result, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
				if err != nil {
					t.Fatal("media result did not survive external validation", string(wire), err)
				}
				var hasImage bool
				var visit func([]p.Node)
				visit = func(nodes []p.Node) {
					for _, node := range nodes {
						hasImage = hasImage || node.Kind == p.ImageNode
						visit(node.Children)
					}
				}
				visit(decoded.Content)
				if !hasImage {
					t.Fatal("target decode lost media semantics")
				}
				if name == "openai-responses" {
					request.Content[1].Status = testValue(t, `{"isError":true}`)
					marked, err := conversion.Request(t.Context(), request, route, nil)
					if err != nil {
						t.Fatal(err)
					}
					blocks := marked.Content[1].Children
					if len(blocks) != 2 || !blocks[0].Payload.IsString("[Tool error] ") || blocks[1].Kind != p.ImageNode || !marked.Content[1].Status.IsZero() {
						t.Fatal("error marker changed media or ordering", blocks)
					}
					if _, err := target.EncodeRequest(t.Context(), marked, p.EvaluationContext{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			request = toolResultRequest(t, `"failed"`, "")
			if name == "openai-chat" || name == "openai-responses" {
				request.Content[1].Payload = testValue(t, `{"message":"failed"}`)
			}
			policy.Rules[0].Enabled = false
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			unresolved, err := disabled.Request(t.Context(), request, route, nil)
			if err == nil {
				_, err = target.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{})
			}
			if (err == nil) != (name == "anthropic") {
				t.Fatal("disabled disposition was repeated or native support rejected", err)
			}
		})
	}
}
