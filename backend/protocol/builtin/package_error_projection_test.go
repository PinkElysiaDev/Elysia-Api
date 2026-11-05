package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageErrorDetailsDisposition(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-chat")
	options := p.EvaluationContext{Values: p.Object{"httpStatus": testValue(t, "429")}}
	failure, err := source.DecodeHTTPFailure(t.Context(), 429, []byte(`{"error":{"type":"rate_limit_error","message":"quota","code":"budget_exhausted","param":"model"}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.EncodeValue(failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			for _, mode := range []string{"compatible", "strict"} {
				policy := declaredRulePolicy(t, target, source, "response-error-details", mode)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Response(t.Context(), failure, route, sink)
				if mode == "strict" {
					if err == nil {
						t.Fatal("strict error projection accepted structured detail loss")
					}
					continue
				}
				if err != nil || projected.Status != failure.Status || p.CheckGenerationOutcome(projected) == nil {
					t.Fatal("error projection changed the failure outcome", err)
				}
				wire, err := target.EncodeResponse(t.Context(), projected, options)
				if err != nil {
					t.Fatal(err)
				}
				if err := target.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(wire), "quota") || !strings.Contains(string(wire), "budget_exhausted") || !strings.Contains(string(wire), "model") {
					t.Fatal("error message lost a known detail", string(wire))
				}
				issues := sink.Issues()
				if len(issues) != 2 || issues[0].Path != "/error/code" || issues[1].Path != "/error/param" || issues[0].Code != p.ConversionDegraded || issues[0].RuleID != "response-error-details" || issues[0].PolicyHash != conversion.Hash {
					t.Fatal("error details lost rule evidence", issues)
				}
				policy.Rules[0].Enabled = false
				disabled, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				retained, err := disabled.Response(t.Context(), failure, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := target.EncodeResponse(t.Context(), retained, options); err == nil {
					t.Fatal("encoder repeated disabled error disposition")
				}
			}
			conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "event-error-details", "compatible"))
			if err != nil {
				t.Fatal(err)
			}
			inputState := p.NewEvaluationState()
			defer inputState.Release()
			frame, err := source.DecodeFrame(t.Context(), testValue(t, `{"error":{"type":"rate_limit_error","message":"quota","code":"budget_exhausted","param":"model"}}`), p.EvaluationContext{State: inputState})
			if err != nil || len(frame.Events) != 1 {
				t.Fatal("source error frame lost its lifecycle", err)
			}
			event := frame.Events[0]
			projected, err := conversion.Event(t.Context(), event, route, &p.DiagnosticSink{})
			if err != nil || projected.Type != p.OperationFailed {
				t.Fatal("stream error disposition changed lifecycle", err)
			}
			state := p.NewEvaluationState()
			defer state.Release()
			frames, err := target.EncodeFrames(t.Context(), projected, p.EvaluationContext{State: state})
			if err != nil || len(frames) == 0 || !strings.Contains(frames[0].JSONString(), "budget_exhausted") {
				t.Fatal("stream error lost its known detail", frames, err)
			}
			snapshot := p.Event{SchemaVersion: p.ProtocolCoreVersion, Source: source.Identity(), Type: p.ResponseFinished, Response: failure}
			projected, err = conversion.Event(t.Context(), snapshot, route, &p.DiagnosticSink{})
			if err != nil || projected.Response.Status != failure.Status || !strings.Contains(projected.Response.Error.JSONString(), "budget_exhausted") {
				t.Fatal("terminal response error lost its detail or outcome", err)
			}
		})
	}
	after, err := p.EncodeValue(failure)
	if err != nil || before != after {
		t.Fatal("error disposition mutated its source", err)
	}
}

func TestPackageErrorDetailsKeepUnknowns(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-chat"), compileBuiltinPackage(t, "anthropic")
	conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "response-error-details", "compatible"))
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	for _, detail := range []string{
		`"code":false`,
		`"code":{"future":1}`,
		`"wire:openai_chat":{"future":true}`,
		`"providerType":"future"`,
	} {
		failure := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Source: source.Identity(), Status: p.StringValue("failed"), Error: testValue(t, `{"category":"rate_limit","message":"quota",`+detail+"}")}
		projected, err := conversion.Response(t.Context(), failure, route, nil)
		if err == nil {
			_, err = target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("undeclared or malformed error detail was discarded", detail)
		}
	}
	for _, detail := range []string{
		`"code":null,"param":""`,
		`"providerType":"new_api_error","code":"vendor_limit"`,
	} {
		failure := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Source: source.Identity(), Error: testValue(t, `{"category":"rate_limit","message":"quota",`+detail+"}")}
		projected, err := conversion.Response(t.Context(), failure, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.EncodeResponse(t.Context(), projected, p.EvaluationContext{}); err != nil {
			t.Fatal("declared detail representation could not encode", err)
		}
	}
}
