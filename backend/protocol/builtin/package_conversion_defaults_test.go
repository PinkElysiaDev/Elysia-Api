package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func declaredRulePolicy(t *testing.T, source, target *p.Compiled, id, mode string) p.ConversionPolicy {
	t.Helper()
	policy := p.DefaultConversionPolicy(source, target)
	for _, rule := range policy.Rules {
		if rule.ID == id {
			if rule.Action != "core_controls" {
				t.Fatal("default retained a provider-specific action", rule)
			}
			policy.Rules, policy.Mode = []p.ConversionRule{rule}, mode
			return policy
		}
	}
	t.Fatal("missing declared default", id)
	return p.ConversionPolicy{}
}

func TestPackageDeclaredRequestDefaults(t *testing.T) {
	source := compileBuiltinPackage(t, "google-generate-content")
	for _, name := range []string{"openai-chat", "anthropic", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			for _, mode := range []string{"compatible", "strict"} {
				policy := declaredRulePolicy(t, source, target, "gemini-safety-settings", mode)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				absent := &p.Request{SchemaVersion: p.ProtocolCoreVersion}
				absentSink := &p.DiagnosticSink{}
				if _, err := conversion.Request(t.Context(), absent, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, absentSink); err != nil || len(absentSink.Issues()) != 0 {
					t.Fatal("absent safety settings triggered a disposition", absentSink.Issues(), err)
				}
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Parameters: p.Object{"gemini_safety_settings": testValue(t, `[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"}]`)}}
				sink := &p.DiagnosticSink{}
				result, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
				if mode == "strict" {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || failure.Issues[0].RuleID != "gemini-safety-settings" {
						t.Fatal("strict safety loss was not attributed", err)
					}
				} else if err != nil || !result.Parameters["gemini_safety_settings"].IsZero() || len(sink.Issues()) != 1 {
					t.Fatal("compatible safety disposition changed", result, sink.Issues(), err)
				}
			}
		})
	}
	target := compileBuiltinPackage(t, "openai-chat")
	policy := declaredRulePolicy(t, source, target, "responses-context", "strict")
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Parameters: p.Object{"responses_previous_response_id": testValue(t, `null`)}}
	result, err := conversion.Request(t.Context(), request, route, nil)
	if err != nil || !result.Parameters["responses_previous_response_id"].IsZero() {
		t.Fatal("null reference normalization became lossy", err)
	}
	request.Parameters["responses_previous_response_id"] = p.StringValue("previous")
	if _, err := conversion.Request(t.Context(), request, route, nil); err == nil {
		t.Fatal("unrestored history reference was discarded")
	}
}

func TestPackageDeclaredResponseModelObservation(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	for _, mode := range []string{"compatible", "strict"} {
		conversion, err := p.ResolveConversion(declaredRulePolicy(t, compiled, compiled, "chat-response-model", mode))
		if err != nil {
			t.Fatal(err)
		}
		route, err := (p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: p.HTTPJSON}).WithResponseModelOrigin(compiled, compiled, []byte(`{"model":"actually-sent"}`), []byte(`{"id":"r"}`))
		if err != nil {
			t.Fatal(err)
		}
		response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed")}
		sink := &p.DiagnosticSink{}
		result, err := conversion.Response(t.Context(), response, route, sink)
		if mode == "strict" {
			if err == nil {
				t.Fatal("strict response repair accepted an inferred model")
			}
		} else if err != nil || !result.Model.IsString("actually-sent") || len(sink.Issues()) != 1 || sink.Issues()[0].Direction != p.DecodeResponse {
			t.Fatal("response model did not use the declared observed field", result, sink.Issues(), err)
		}
		response.Model = testValue(t, `null`)
		if result, err := conversion.Response(t.Context(), response, route, nil); err != nil || !result.Model.IsNull() {
			t.Fatal("explicit null model was repaired", result, err)
		}
	}
}
