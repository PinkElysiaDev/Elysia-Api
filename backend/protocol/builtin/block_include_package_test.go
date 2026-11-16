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
			policy := p.DefaultConversionPolicy(source, target)
			policy.Mode = "strict"
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "64"), "wire:openai_responses": testValue(t, `{"include":["reasoning.encrypted_content"]}`)},
				Content:      []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}},
				ClientOutput: &p.ClientOutput{RequireContinuationState: true, ResponsesStorage: &p.ResponsesStorageIntent{}}}
			prepared, err := conversion.Request(t.Context(), request, route, nil)
			if err != nil || !prepared.ClientOutput.RequireContinuationState || !prepared.Parameters["wire:openai_responses"].IsZero() || request.Parameters["wire:openai_responses"].IsZero() {
				t.Fatal("include disposition changed source intent or failed to resolve it", prepared, err)
			}
			wire, err := target.EncodeRequest(t.Context(), prepared, p.EvaluationContext{ClientOutput: prepared.ClientOutput})
			if err != nil || strings.Contains(string(wire), `"include"`) {
				t.Fatal("target leaked a foreign include field", string(wire), err)
			}
			unknown := request.Clone()
			unknown.Parameters["wire:openai_responses"] = testValue(t, `{"include":["future.selector"]}`)
			projected, err := conversion.Request(t.Context(), unknown, route, nil)
			if err == nil {
				_, err = target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			}
			if err == nil {
				t.Fatal("unknown selector was treated as resolved")
			}
			for index := range policy.Rules {
				if policy.Rules[index].ID == "reasoning-controls" {
					policy.Rules[index].Enabled = false
				}
			}
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
