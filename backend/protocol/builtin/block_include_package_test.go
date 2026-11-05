package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestBlockPackageIncludeDisposition(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			policy := declaredRulePolicy(t, source, target, "responses-include", "strict")
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "64")},
				Content:      []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}},
				ClientOutput: &p.ClientOutput{RawResponsesInclude: testValue(t, `["reasoning.encrypted_content"]`), RequireContinuationState: true, ResponsesStorage: &p.ResponsesStorageIntent{}}}
			prepared, err := conversion.Request(t.Context(), request, route, nil)
			if err != nil || !prepared.ClientOutput.ResponsesIncludeResolved || !prepared.ClientOutput.RequireContinuationState || prepared.ClientOutput.RawResponsesInclude != request.ClientOutput.RawResponsesInclude || request.ClientOutput.ResponsesIncludeResolved {
				t.Fatal("include disposition changed source intent or failed to resolve it", prepared, err)
			}
			wire, err := target.EncodeRequest(t.Context(), prepared, p.EvaluationContext{ClientOutput: prepared.ClientOutput})
			if err != nil || strings.Contains(string(wire), `"include"`) {
				t.Fatal("target leaked a foreign include field", string(wire), err)
			}
			unknown := request.Clone()
			unknown.ClientOutput.RawResponsesInclude = testValue(t, `["future.selector"]`)
			if _, err := conversion.Request(t.Context(), unknown, route, nil); err == nil {
				t.Fatal("unknown selector was treated as resolved")
			}
			policy.Rules[0].Enabled = false
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			unresolved, err := disabled.Request(t.Context(), request, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{}); err == nil {
				t.Fatal("disabled include rule was replaced by an encoding fallback")
			}
		})
	}
}
