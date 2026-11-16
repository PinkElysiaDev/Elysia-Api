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
			// Check every wrapper role natively, and each cross-family target
			// with the SDK's user role. Lossless normalization has no mode axis.
			if role != `"user"` && name != Gemini {
				continue
			}
			t.Run(name+"/"+role, func(t *testing.T) {
				to := shippedProjectionProtocol(t, name)
				r, err := from.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
				if err != nil {
					t.Fatal(err)
				}
				if r.Content[0].Role != p.StringValue("system") || !r.Parameters["wire:gemini"].IsZero() {
					t.Fatal("system instruction wrapper role became foreign semantics", r.Parameters, r.Content[0])
				}
				policy := p.DefaultConversionPolicy(from, to)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				out, err := conversion.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
				if err != nil {
					t.Fatal(err)
				}
				roundtrip, err := to.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
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
					wire, err = to.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, wire, strings.Replace(body, "system prefix", "edited prefix", 1))
				}
			})
		}
	}
}

func TestGeminiSystemInstructionRoleValidationAndExtensions(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	for _, role := range []string{`42`, `false`, `[]`, `{}`} {
		_, err := from.DecodeRequest(t.Context(), []byte(`{"systemInstruction":{"role":`+role+`,"parts":[{"text":"prefix"}]},"contents":[{"parts":[{"text":"question"}]}]}`), fixtureOptions(p.EvaluationContext{}))
		requireContextIssue(t, err, p.InvalidInput, "/systemInstruction/role")
	}
	for _, field := range []string{`,"vendor":null`, `,"cache_control":{"type":"ephemeral"}`} {
		body := `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}]` + field + `},"contents":[{"parts":[{"text":"question"}]}]}`
		r, err := from.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := from.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		if _, err := to.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})); err == nil {
			t.Fatal("unknown system instruction state silently removed")
		}
	}
}
