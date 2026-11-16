package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const disabledGeminiSafety = `[{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"BLOCK_NONE"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]`

func TestGeminiSafetySettingsProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})
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
							if policy.Rules[i].ID == "safety-settings" {
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
						requireContextIssue(t, err, p.ConversionRejected, "/parameters/safety")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wire, err := to.EncodeRequest(t.Context(), out, options)
					if name != Gemini && raw != "" && mode == "disabled" {
						requireContextIssue(t, err, p.UnsupportedCapability, "/parameters/safety")
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
							if len(issues) != 1 || issues[0].RuleID != "safety-settings" || issues[0].PolicyHash != c.Hash || issues[0].Path != "/parameters/safety" {
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
					if raw != "" && r.Parameters["safety"].IsZero() {
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
		_, err := from.DecodeRequest(t.Context(), []byte(`{"contents":[],"safetySettings":`+tc.raw+`}`), fixtureOptions(p.EvaluationContext{}))
		wirePath := tc.path
		if tc.path == "/safetySettings/1/category" {
			wirePath = "/safetySettings"
		}
		requireContextIssue(t, err, p.InvalidInput, wirePath)
		// Preview validates canonical settings independently of the source wire shape.
		canonical := strings.ReplaceAll(tc.raw, "HARM_CATEGORY_HARASSMENT", "harassment")
		canonical = strings.ReplaceAll(canonical, "BLOCK_NONE", "none")
		canonical = strings.ReplaceAll(canonical, "OFF", "off")
		value := testValue(t, `{"schemaVersion":2,"source":{},"content":[],"parameters":{"safety":`+canonical+`}}`)
		_, err = c.ApplyValue(t.Context(), p.ConversionRequest, value, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
		requireContextIssue(t, err, p.InvalidInput, strings.Replace(tc.path, "/safetySettings", "/parameters/safety", 1))
	}
	for _, tc := range []struct{ raw, path string }{
		{strings.Replace(disabledGeminiSafety, "BLOCK_NONE", "BLOCK_LOW_AND_ABOVE", 1), "/safetySettings/0/threshold"},
		{strings.Replace(disabledGeminiSafety, "BLOCK_NONE", "HARM_BLOCK_THRESHOLD_UNSPECIFIED", 1), "/safetySettings/0/threshold"},
		{strings.Replace(disabledGeminiSafety, "HARM_CATEGORY_HATE_SPEECH", "FUTURE_CATEGORY", 1), "/safetySettings/0/category"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE","vendor":null}]`, "/safetySettings/0/vendor"},
		{`[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE","vendor/~":null}]`, "/safetySettings/0/vendor~1~0"},
	} {
		r, err := from.DecodeRequest(t.Context(), []byte(`{"contents":[{"parts":[{"text":"test"}]}],"safetySettings":`+tc.raw+`}`), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
		if err != nil {
			t.Fatal(err)
		}
		sink := &p.DiagnosticSink{}
		projected, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
		if r.Parameters["safety"].IsZero() {
			if err == nil {
				_, err = to.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
			}
			if err == nil {
				t.Fatal("unknown source safety extension was discarded", tc.raw)
			}
		} else {
			requireContextIssue(t, err, p.ConversionRejected, strings.Replace(tc.path, "/safetySettings", "/parameters/safety", 1))
		}
		if len(sink.Issues()) != 0 {
			t.Fatal("partially normalized a rejected safety configuration", sink.Issues())
		}
		r.Native = nil
		if _, err := from.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})); err != nil {
			t.Fatal("native settings lost", err)
		}
	}
}
