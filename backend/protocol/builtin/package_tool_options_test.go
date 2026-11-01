package builtin

import (
	"errors"
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageDeclaredToolOptions(t *testing.T) {
	packages := map[string]*p.Compiled{}
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		packages[name] = compileBuiltinPackage(t, name)
	}
	for _, fixture := range []struct {
		source, target, raw, want string
		isLoss                    bool
	}{
		{"openai-chat", "openai-responses", "omitted", "false", false},
		{"anthropic", "openai-responses", "null", "false", false},
		{"openai-responses", "openai-responses", "null", "null", false},
		{"openai-responses", "openai-chat", "omitted", "omitted", true},
		{"openai-responses", "anthropic", "null", "omitted", true},
		{"openai-chat", "anthropic", "true", "true", false},
		{"anthropic", "google-generate-content", "true", "omitted", true},
		{"openai-chat", "google-generate-content", "false", "omitted", false},
	} {
		t.Run(fixture.source+"/"+fixture.target+"/"+fixture.raw, func(t *testing.T) {
			source, target := packages[fixture.source], packages[fixture.target]
			codec := map[string]string{"openai-chat": Chat, "openai-responses": Responses, "anthropic": Anthropic}[fixture.source]
			raw := strictToolRequest(codec, fixture.raw)
			request, err := source.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := p.EncodeValue(request)
			hint := request.Tools[0].Options["strictDefault"]
			wantDefault := "disabled"
			if fixture.source == "openai-responses" {
				wantDefault = "automatic"
			}
			if fixture.raw == "omitted" || fixture.raw == "null" {
				if !hint.IsString(wantDefault) {
					t.Fatal("source default was not declared", hint)
				}
			} else if !hint.IsZero() {
				t.Fatal("explicit strictness also acquired a default", hint)
			}
			policy := declaredRulePolicy(t, source, target, "tool-options", "compatible")
			policy.Rules = append(policy.Rules, declaredRulePolicy(t, source, target, "responses-storage", "compatible").Rules...)
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
			got := result.Tools[0].Options["strict"]
			if fixture.want == "omitted" && !got.IsZero() || fixture.want != "omitted" && got.JSONString() != fixture.want || result.Tools[0].InputSchema != request.Tools[0].InputSchema {
				t.Fatal("tool schema or strictness changed incorrectly", result.Tools[0])
			}
			hasLoss := false
			for _, issue := range sink.Issues() {
				if issue.RuleID != "tool-options" || issue.Path != "/tools/0/options/strict" {
					t.Fatal("lost tool rule attribution", issue)
				}
				hasLoss = hasLoss || issue.Fidelity == "lossy_compatible"
			}
			if hasLoss != fixture.isLoss {
				t.Fatal("equivalence and loss were conflated", sink.Issues())
			}
			result.Native = nil
			wire, err := target.EncodeRequest(t.Context(), result, p.EvaluationContext{})
			if err != nil {
				t.Fatal("declared tool disposition did not rebuild", string(wire), err)
			}
			decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
			if err != nil || len(decoded.Tools) != 1 || decoded.Tools[0].Options["strict"] != got {
				t.Fatal("wire tool constraints differ from the resolved intent", string(wire), err)
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			_, err = strict.Request(t.Context(), request, route, nil)
			if (err != nil) != fixture.isLoss {
				t.Fatal("strict accepted a loss or rejected equivalence", err)
			}
			if fixture.isLoss {
				var failure *p.ConversionError
				if !errors.As(err, &failure) || !failure.CandidateOnly {
					t.Fatal("strict tool loss was not candidate scoped", err)
				}
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("target tool projection changed caller input")
			}
		})
	}
}

func TestPackageToolOptionsDisabledAndStreaming(t *testing.T) {
	source := compileBuiltinPackage(t, "anthropic")
	for _, name := range []string{"anthropic", "openai-chat", "openai-responses", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			policy := declaredRulePolicy(t, source, target, "tool-options", "compatible")
			policy.Rules = append(policy.Rules, declaredRulePolicy(t, source, target, "responses-storage", "compatible").Rules...)
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			for _, isStream := range []bool{false, true} {
				raw := fmt.Sprintf(`{"model":"m","max_tokens":100,"stream":%t,"messages":[],"tools":[{"name":"f","input_schema":{"type":"object"},"eager_input_streaming":true}]}`, isStream)
				request, err := source.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				result, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
				if err != nil {
					t.Fatal(err)
				}
				if !result.Tools[0].Options["eager_input_streaming"].IsZero() != (isStream && name == "anthropic") {
					t.Fatal("stream timing hint has the wrong target owner", result.Tools[0])
				}
				strictPolicy := policy
				strictPolicy.Mode = "strict"
				strict, err := p.ResolveConversion(strictPolicy)
				if err != nil {
					t.Fatal(err)
				}
				_, err = strict.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
				if (err != nil) != (isStream && name != "anthropic") {
					t.Fatal("strict stream hint disposition changed", err)
				}
				if name != "anthropic" {
					request.Native = nil
					disabledPolicy := policy
					disabledPolicy.Rules = append([]p.ConversionRule(nil), policy.Rules...)
					disabledPolicy.Rules[0].Enabled = false
					disabled, err := p.ResolveConversion(disabledPolicy)
					if err != nil {
						t.Fatal(err)
					}
					unresolved, err := disabled.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
					if err == nil {
						_, err = target.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{})
					}
					if err == nil {
						t.Fatal("encoder repeated the disabled disposition")
					}
					var failure *p.ConversionError
					if !errors.As(err, &failure) || failure.Issues[0].Path != "/tools" {
						t.Fatal("disabled tool rule failed at an unrelated boundary", err)
					}
				}
			}
		})
	}
}

func TestPackageToolOptionsRejectUnknownAndInvalidHints(t *testing.T) {
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic"} {
		compiled := compileBuiltinPackage(t, name)
		codec := map[string]string{"openai-chat": Chat, "openai-responses": Responses, "anthropic": Anthropic}[name]
		raw := strictToolRequest(codec, `"true"`)
		if _, err := compiled.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{}); err == nil {
			t.Fatal("invalid external hint passed its declared type", name)
		}
		request, err := compiled.DecodeRequest(t.Context(), []byte(strictToolRequest(codec, "true")), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		request.Native = nil
		request.Tools[0].Options["future"] = p.StringValue("preserve")
		if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
			t.Fatal("unknown tool option disappeared during rebuilding", name)
		}
	}
}
