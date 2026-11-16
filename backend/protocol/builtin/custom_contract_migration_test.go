package builtin

import (
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiSystemInstructionOldCustomCombination(t *testing.T) {
	d, _ := testCompiled(t, Gemini).Package()
	d.Spec.ID = "custom-gemini-system"
	d.Mapping.Samples = append(d.Mapping.Samples, p.Sample{
		ID: "sdk-system-role", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":32}}`),
		Expected: testValue(t, `{"schemaVersion":2,"clientOutput":{"responsesStorage":{"sourceCodec":"gemini","requested":false,"effective":false}},"model":"m","parameters":{"max_output_tokens":32},"content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"prefix"}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}]}`),
	})
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := p.EncodeValue(d)
	from, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if r := p.Verify(t.Context(), from); !r.Passed {
		t.Fatal(r.Issues)
	}
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, name)
		report := p.VerifyBindingCombination(t.Context(), from, to, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
		if !report.Passed {
			t.Fatal(name, report.Issues)
		}
		found := false
		for _, check := range report.Checks {
			if check.SampleID == "sdk-system-role" && check.Passed {
				found = true
			}
		}
		if !found {
			t.Fatal("system role sample was not verified", name, report.Checks)
		}
	}
}

func TestGeminiSafetySettingsCustomPreviewAndCombination(t *testing.T) {
	d, _ := testCompiled(t, Gemini).Package()
	d.Spec.ID = "custom-gemini-safety"
	body := `{"contents":[{"role":"user","parts":[{"text":"test"}]}],"generationConfig":{"maxOutputTokens":32},"safetySettings":` + disabledGeminiSafety + `}`
	d.Mapping.Samples = append(d.Mapping.Samples, p.Sample{
		ID: "disabled-safety", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, body),
		Expected: testValue(t, `{"schemaVersion":2,"clientOutput":{"responsesStorage":{"sourceCodec":"gemini","requested":false,"effective":false}},"model":"m","parameters":{"max_output_tokens":32,"safety":[{"category":"hate-speech","threshold":"none"},{"category":"dangerous-content","threshold":"none"},{"category":"harassment","threshold":"none"},{"category":"sexual-content","threshold":"none"},{"category":"civic-integrity","threshold":"none"}]},"content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"test"}]}]}`),
	})
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := p.EncodeValue(d)
	from, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), from); !report.Passed {
		t.Fatal(report.Issues)
	}
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, name)
		report := p.VerifyBindingCombination(t.Context(), from, to, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
		if !report.Passed {
			t.Fatal(name, report.Issues)
		}
		found := false
		for _, check := range report.Checks {
			if check.SampleID == "disabled-safety" && check.Passed {
				found = true
			}
		}
		if !found {
			t.Fatal("safety request missing from combination evidence", name)
		}
		req, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		c, err := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
		if err != nil {
			t.Fatal(err)
		}
		route := p.ConversionContext{Source: from.Identity(), Target: to.Identity()}
		runtimeSink, previewSink := &p.DiagnosticSink{}, &p.DiagnosticSink{}
		out, err := c.Request(t.Context(), req, route, runtimeSink)
		if err != nil {
			t.Fatal(err)
		}
		value, _ := p.EncodeValue(req)
		preview, err := c.ApplyValue(t.Context(), p.ConversionRequest, value, route, previewSink)
		if err != nil {
			t.Fatal(err)
		}
		var previewRequest p.Request
		if err := preview.Decode(&previewRequest); err != nil {
			t.Fatal(err)
		}
		// Request additionally installs the policy's usage-collection preference.
		// Compare the parameter/content projection handled by this action.
		if !reflect.DeepEqual(out.Parameters, previewRequest.Parameters) || !reflect.DeepEqual(out.Content, previewRequest.Content) {
			t.Fatal("preview parameter/content projection differs from runtime")
		}
		if !reflect.DeepEqual(runtimeSink.Issues(), previewSink.Issues()) {
			t.Fatal("preview diagnostics differ from runtime")
		}
	}
	// Authored output still has to satisfy the independent target Spec.
	mapping := d.Mapping.Directions[p.EncodeRequest]
	mapping.Program = p.Expression{Op: "read"}
	d.Mapping.Directions[p.EncodeRequest] = mapping
	handwritten, issues := compilePackageDocument(t, d)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	request, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := handwritten.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err == nil {
		err = handwritten.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
	}
	if err == nil {
		t.Fatal("authored core passthrough bypassed the final wire contract")
	}
}

func TestResponsesMessagePhaseOldCustomRequestProjection(t *testing.T) {
	document, _ := testCompiled(t, Responses).Package()
	document.Spec.ID, document.Spec.Family = "custom-phase-source", "authored-phase-family"
	from, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			request, err := from.DecodeRequest(t.Context(), []byte(`{"model":"m","max_output_tokens":64,"store":false,"input":[{"role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"OK"}]}]}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			policy := p.DefaultConversionPolicy(from, to)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].ID == "history-response-metadata" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
			if target != Responses && mode == "strict" {
				if err == nil || !strings.Contains(err.Error(), "/phase") {
					t.Fatal(target, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if mode == "disabled" && target != Responses {
				if err == nil || !strings.Contains(err.Error(), "/phase") {
					t.Fatal("request ignored disabled rule", target, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(target, err)
			}
			kept := strings.Contains(string(wire), `"phase":"commentary"`)
			if kept != (target == Responses) {
				t.Fatal(target, string(wire))
			}
			if target != Responses && !sinkHas(sink, "/phase") {
				t.Fatal("history phase loss lacks diagnostic")
			}
		}
	}
}

func TestResponsesMessagePhaseCombinationEvidence(t *testing.T) {
	definition, _ := testCompiled(t, Responses).Package()
	definition.Mapping.Samples = append(definition.Mapping.Samples, p.Sample{
		ID: "message-phase-evidence", Direction: p.DecodeResponse,
		Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"OK","annotations":[]}]}]}`),
		Expected: testValue(t, `{"schemaVersion":2,"id":"r","model":"m","status":"completed","attributes":{"finishReason":"stop","created_at":1},"content":[{"kind":"message","id":"msg","status":"completed","role":"assistant","metadata":[{"codec":"responses","sourceCodec":"responses","location":"message","name":"phase","meaning":"information","path":"/output/0/phase","value":"commentary"}],"children":[{"kind":"text","payload":"OK"}]}]}`),
	})
	compiler, _ := p.NewCompiler(p.DefaultLimits(), nil)
	raw, _ := p.EncodeValue(definition)
	upstream, issues := compiler.Compile(raw.Bytes())
	if upstream == nil {
		t.Fatal(issues)
	}
	if report := p.Verify(t.Context(), upstream); !report.Passed {
		t.Fatal("independent oracle invalid", report.Issues)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			policy := p.DefaultConversionPolicy(to, upstream)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].ID == "response-metadata" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			report := p.VerifyBindingCombination(t.Context(), to, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, conversion)
			found := false
			for _, check := range report.Checks {
				if check.SampleID == "message-phase-evidence" {
					found = true
					if check.Skipped || check.Passed != (target == Responses || mode == "compatible") {
						t.Fatal(target, mode, check, report.Issues)
					}
				}
			}
			if !found {
				t.Fatal("phase evidence not executed")
			}
		}
	}
}
