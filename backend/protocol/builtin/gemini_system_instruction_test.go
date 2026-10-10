package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiSystemInstructionRoleAcrossInstalledCodecs(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	for _, role := range []string{`"user"`, `"model"`, `""`, `null`} {
		body := `{"systemInstruction":{"role":` + role + `,"parts":[{"text":"system prefix"}]},"contents":[{"role":"user","parts":[{"text":"question"}]}],"generationConfig":{"maxOutputTokens":32}}`
		for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
			for _, mode := range []string{"compatible", "strict"} {
				t.Run(name+"/"+mode+"/"+role, func(t *testing.T) {
					to := shippedProjectionProtocol(t, name)
					r, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
					if err != nil {
						t.Fatal(err)
					}
					if r.Content[0].Role != p.StringValue("system") || !r.Parameters["wire:gemini"].IsZero() {
						t.Fatal("system instruction wrapper role became foreign semantics", r.Parameters, r.Content[0])
					}
					policy := p.DefaultConversionPolicy(from, to)
					policy.Mode = mode
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					out, err := conversion.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := to.EncodeRequest(t.Context(), out, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
					if err != nil {
						t.Fatal(err)
					}
					if err := to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
						t.Fatal(err)
					}
					roundtrip, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
					if err != nil {
						t.Fatal(err)
					}
					if len(roundtrip.Content) != 2 || roundtrip.Content[0].Role != p.StringValue("system") || roundtrip.Content[0].Children[0].Payload != p.StringValue("system prefix") || roundtrip.Content[1].Role != p.StringValue("user") {
						t.Fatal("system authority or content changed", string(wire))
					}
					if len(sink.Issues()) != 0 {
						t.Fatal("lossless role decoding diagnosed as degradation", sink.Issues())
					}
					if name == Gemini {
						sameJSON(t, wire, body)
						out.Content[0].Children[0].Payload = p.StringValue("edited prefix")
						wire, err = to.EncodeRequest(t.Context(), out, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
						if err != nil {
							t.Fatal(err)
						}
						sameJSON(t, wire, strings.Replace(body, "system prefix", "edited prefix", 1))
					}
				})
			}
		}
	}
}

func TestGeminiSystemInstructionRoleValidationAndExtensions(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	for _, role := range []string{`42`, `false`, `[]`, `{}`} {
		_, err := from.DecodeRequest(t.Context(), []byte(`{"systemInstruction":{"role":`+role+`,"parts":[{"text":"prefix"}]},"contents":[{"parts":[{"text":"question"}]}]}`), p.EvaluationContext{})
		requireContextIssue(t, err, p.InvalidInput, "/systemInstruction/role")
	}
	for _, field := range []string{`,"vendor":null`, `,"cache_control":{"type":"ephemeral"}`} {
		body := `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}]` + field + `},"contents":[{"parts":[{"text":"question"}]}]}`
		r, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := from.EncodeRequest(t.Context(), r, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		if _, err := to.EncodeRequest(t.Context(), r, p.EvaluationContext{Scope: p.Scope{Model: "m"}}); err == nil {
			t.Fatal("unknown system instruction state silently removed")
		}
	}
}

func TestGeminiSystemInstructionOldCustomCombination(t *testing.T) {
	d := shippedProjectionProtocol(t, Gemini).Definition()
	d.ID, d.Requires = "old-gemini-system-copy", nil
	mapping := d.Directions[p.DecodeRequest]
	mapping.After = &p.Expression{Op: "read", Path: ""}
	d.Directions[p.DecodeRequest] = mapping
	d.Samples = append(d.Samples, p.Sample{
		ID: "sdk-system-role", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":32}}`),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"model":"m","parameters":{"max_output_tokens":32},"content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"prefix"}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}]}`),
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
