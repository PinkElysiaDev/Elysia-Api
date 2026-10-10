package builtin

import (
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const disabledGeminiSafety = `[{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]`

func TestGeminiSafetySettingsProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	options := p.EvaluationContext{Scope: p.Scope{Model: "m"}}
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, name)
		for _, raw := range []string{"", "null", "[]", disabledGeminiSafety, strings.ReplaceAll(disabledGeminiSafety, "BLOCK_NONE", "OFF")} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(name+"/"+mode+"/"+raw, func(t *testing.T) {
					body := `{"contents":[{"role":"user","parts":[{"text":"test"}]}],"generationConfig":{"maxOutputTokens":32}`
					if raw != "" {
						body += `,"safetySettings":` + raw
					}
					body += "}"
					r, err := from.DecodeRequest(t.Context(), []byte(body), options)
					if err != nil {
						t.Fatal(err)
					}
					policy := p.DefaultConversionPolicy(from, to)
					if mode == "disabled" {
						for i := range policy.Rules {
							if policy.Rules[i].ID == "gemini-safety-settings" {
								policy.Rules[i].Enabled = false
							}
						}
					} else {
						policy.Mode = mode
					}
					c, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					out, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
					loss := raw != "" && raw != "null" && raw != "[]"
					if name != Gemini && loss && mode == "strict" {
						requireContextIssue(t, err, p.ConversionRejected, "/safetySettings")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wire, err := to.EncodeRequest(t.Context(), out, options)
					if name != Gemini && raw != "" && mode == "disabled" {
						requireContextIssue(t, err, p.UnsupportedCapability, "/parameters/gemini_safety_settings")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if name == Gemini {
						sameJSON(t, wire, body)
						if len(sink.Issues()) != 0 {
							t.Fatal(sink.Issues())
						}
					} else {
						if strings.Contains(string(wire), "safety") {
							t.Fatal("Gemini filter settings leaked upstream", string(wire))
						}
						if raw != "" {
							issues := sink.Issues()
							if len(issues) != 1 || issues[0].RuleID != "gemini-safety-settings" || issues[0].PolicyHash != c.Hash || issues[0].Path != "/safetySettings" {
								t.Fatal(issues)
							}
							if loss && (issues[0].Fidelity != "lossy_compatible" || issues[0].Severity != p.SeverityWarning) {
								t.Fatal(issues)
							}
							if !loss && (issues[0].Code != p.ConversionNormalized || issues[0].Fidelity != "preserved") {
								t.Fatal(issues)
							}
						}
					}
					if raw != "" && r.Parameters["gemini_safety_settings"].IsZero() {
						t.Fatal("source request mutated")
					}
				})
			}
		}
	}
}

func TestGeminiSafetySettingsRejectConstraintsAndInvalidTypes(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
	for _, tc := range []struct{ raw, path string }{
		{`42`, "/safetySettings"}, {`{}`, "/safetySettings"}, {`[null]`, "/safetySettings/0"},
		{`[{"category":42,"threshold":"BLOCK_NONE"}]`, "/safetySettings/0/category"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":false}]`, "/safetySettings/0/threshold"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"}]`, "/safetySettings/1/category"},
	} {
		_, err := from.DecodeRequest(t.Context(), []byte(`{"contents":[],"safetySettings":`+tc.raw+`}`), p.EvaluationContext{})
		requireContextIssue(t, err, p.InvalidInput, tc.path)
		// Authored semantic mappings and preview use the identical validator.
		value := testValue(t, `{"schemaVersion":1,"source":{},"content":[],"parameters":{"gemini_safety_settings":`+tc.raw+`}}`)
		_, err = c.ApplyValue(t.Context(), p.ConversionRequest, value, p.ConversionContext{}, nil)
		requireContextIssue(t, err, p.InvalidInput, tc.path)
	}
	for _, tc := range []struct{ raw, path string }{
		{strings.Replace(disabledGeminiSafety, "BLOCK_NONE", "BLOCK_LOW_AND_ABOVE", 1), "/safetySettings/0/threshold"},
		{strings.Replace(disabledGeminiSafety, "BLOCK_NONE", "HARM_BLOCK_THRESHOLD_UNSPECIFIED", 1), "/safetySettings/0/threshold"},
		{strings.Replace(disabledGeminiSafety, "HARM_CATEGORY_HATE_SPEECH", "FUTURE_CATEGORY", 1), "/safetySettings/0/category"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE","vendor":null}]`, "/safetySettings/0/vendor"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE","vendor/~":null}]`, "/safetySettings/0/vendor~1~0"},
	} {
		r, err := from.DecodeRequest(t.Context(), []byte(`{"contents":[{"parts":[{"text":"test"}]}],"safetySettings":`+tc.raw+`}`), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		sink := &p.DiagnosticSink{}
		_, err = c.Request(t.Context(), r, p.ConversionContext{}, sink)
		requireContextIssue(t, err, p.ConversionRejected, tc.path)
		if len(sink.Issues()) != 0 {
			t.Fatal("partially normalized a rejected safety configuration", sink.Issues())
		}
		if _, err := from.EncodeRequest(t.Context(), r, p.EvaluationContext{Scope: p.Scope{Model: "m"}}); err != nil {
			t.Fatal("native settings lost", err)
		}
	}
}

func TestGeminiSafetySettingsCustomPreviewAndCombination(t *testing.T) {
	d := shippedProjectionProtocol(t, Gemini).Definition()
	d.ID, d.Requires = "old-gemini-safety-copy", nil
	mapping := d.Directions[p.DecodeRequest]
	mapping.After = &p.Expression{Op: "read", Path: ""}
	d.Directions[p.DecodeRequest] = mapping
	body := `{"contents":[{"role":"user","parts":[{"text":"test"}]}],"generationConfig":{"maxOutputTokens":32},"safetySettings":` + disabledGeminiSafety + `}`
	d.Samples = append(d.Samples, p.Sample{
		ID: "disabled-safety", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, body),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"model":"m","parameters":{"max_output_tokens":32,"gemini_safety_settings":`+disabledGeminiSafety+`},"content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"test"}]}]}`),
	})
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
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
	// A same-family handwritten target owns its explicit parameter mapping.
	d.ID = "handwritten-gemini-target"
	d.Directions[p.EncodeRequest] = p.Mapping{Transform: &p.Expression{Op: "read", Path: ""}}
	raw, _ = p.EncodeValue(d)
	to, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, rule := range p.DefaultConversionPolicy(from, to).Rules {
		if rule.Action == "gemini_safety_settings" {
			t.Fatal("guessed handwritten encoder capability", rule)
		}
	}
}
