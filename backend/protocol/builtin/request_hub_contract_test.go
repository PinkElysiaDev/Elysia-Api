package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesTextConfigurationKeepsOwnOwner(t *testing.T) {
	source := testCompiled(t, Responses)
	const text = `{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"score":{"type":"integer"}}},"strict":true},"verbosity":"low","vendor":9007199254740993}`
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","store":false,"input":"hello","text":`+text+`}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if !request.Parameters["responses_text"].IsZero() {
		t.Fatal("source emitted a retired text wrapper")
	}
	owner, err := request.Parameters["wire:openai_responses"].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, owner["text"].Bytes(), text)
	request.Native = nil
	wire, err := source.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := testValue(t, string(wire)).ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, fields["text"].Bytes(), text)
	for _, name := range []string{Chat, Anthropic, Gemini} {
		target := testCompiled(t, name)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil || !strings.Contains(err.Error(), "foreign protocol-specific owner") {
			t.Fatal("foreign target discarded or interpreted Responses text configuration", name, err)
		}
	}
}

func TestRequestParametersRequireDeclaredTargetMapping(t *testing.T) {
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		compiled := testCompiled(t, name)
		for _, key := range []string{"unregistered_control", "responses_text"} {
			for _, raw := range []string{`null`, `{}`, `false`, `"must-not-disappear"`} {
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{key: testValue(t, raw)}, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}}
				before, _ := p.EncodeValue(request)
				wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
				if err == nil || len(wire) != 0 || !strings.Contains(err.Error(), "/parameters") {
					t.Fatal("undeclared parameter silently disappeared", name, key, raw, err)
				}
				after, _ := p.EncodeValue(request)
				if before != after {
					t.Fatal("parameter rejection changed the source")
				}
			}
		}
	}
}

func TestReasoningRemaindersStayWithTheirSource(t *testing.T) {
	for _, sample := range []struct {
		name, before, after, field, effort string
	}{
		{"openai-responses", `{"model":"m","store":false,"input":"hi","reasoning":`, `}`, "reasoning", "effort"},
		{"anthropic", `{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":"hi"}],"output_config":`, `}`, "output_config", "effort"},
		{"google-generate-content", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":`, `}}`, "thinkingConfig", "thinkingLevel"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := compileBuiltinPackage(t, sample.name)
			for _, raw := range []string{`null`, `{}`, `{"vendor":9007199254740993}`, `{"` + sample.effort + `":"high","vendor":9007199254740993}`} {
				t.Run(raw, func(t *testing.T) {
					request, err := source.DecodeRequestValue(t.Context(), testValue(t, sample.before+raw+sample.after), fixtureOptions(p.EvaluationContext{Values: p.Object{"model": p.StringValue("m")}}))
					if err != nil {
						t.Fatal(err)
					}
					for _, retired := range []string{"responses_reasoning", "anthropic_thinking", "anthropic_output_config", "gemini_thinking"} {
						if !request.Parameters[retired].IsZero() {
							t.Fatal("source produced a retired cross-protocol wrapper", retired)
						}
					}
					request.Native = nil
					wire, err := source.EncodeRequestValue(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
					if err != nil || !equivalentJSON(readReasoningField(t, wire, sample.field), testValue(t, raw)) {
						t.Fatal("own reasoning remainder was lost without Native", wire, err)
					}
					if strings.Contains(raw, `"high"`) {
						edited := request.Clone()
						edited.Reasoning.Effort = p.StringValue("low")
						wire, err = source.EncodeRequestValue(t.Context(), edited, fixtureOptions(p.EvaluationContext{}))
						want := strings.Replace(raw, `"high"`, `"low"`, 1)
						if err != nil || !equivalentJSON(readReasoningField(t, wire, sample.field), testValue(t, want)) || !request.Reasoning.Effort.IsString("high") {
							t.Fatal("owned extension restored an old canonical control or mutated the source", wire, err)
						}
					}
					if !strings.Contains(raw, "vendor") {
						return
					}
					for _, targetName := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
						if targetName == sample.name {
							continue
						}
						target := compileBuiltinPackage(t, targetName)
						conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
						if err != nil {
							t.Fatal(err)
						}
						projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m"}, nil)
						if err == nil {
							_, err = target.EncodeRequestValue(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
						}
						if err == nil || !strings.Contains(err.Error(), "foreign protocol-specific owner") {
							t.Fatal("foreign target interpreted or discarded an owned reasoning extension", targetName, err)
						}
					}
				})
			}
		})
	}
}

func readReasoningField(t *testing.T, wire p.Value, field string) p.Value {
	t.Helper()
	fields, err := wire.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	if field == "thinkingConfig" {
		fields, err = fields["generationConfig"].ReadObject()
		if err != nil {
			t.Fatal(err)
		}
	}
	return fields[field]
}

func TestResponsesContinuationSelectionUsesCanonicalIntent(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{`null`, `[]`, `["future"]`, `["reasoning.encrypted_content","future"]`} {
		request, err := compiled.DecodeRequestValue(t.Context(), testValue(t, `{"model":"m","input":"hi","include":`+raw+`}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		request.Native = nil
		for _, required := range []bool{false, true} {
			request.ClientOutput.RequireContinuationState = required
			options := p.EvaluationContext{ClientOutput: request.ClientOutput}
			wire, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if err != nil {
				t.Fatal(raw, err)
			}
			fields, _ := wire.ReadObject()
			if strings.Contains(fields["include"].JSONString(), "reasoning.encrypted_content") != required || strings.Contains(fields["include"].JSONString(), "future") != strings.Contains(raw, "future") {
				t.Fatal("owned selector layout overrode canonical intent or lost an unknown selector", wire)
			}
			if err := compiled.ValidateRequestOutput(t.Context(), wire, options); err != nil {
				t.Fatal(err)
			}
			fields["include"] = testValue(t, `[]`)
			if !required {
				fields["include"] = testValue(t, `["reasoning.encrypted_content"]`)
			}
			changed, err := p.EncodeValue(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.ValidateRequestOutput(t.Context(), changed, options); err == nil || !strings.Contains(err.Error(), "/include") {
				t.Fatal("a final wire edit changed the canonical continuation selection", changed, err)
			}
		}
	}
}
