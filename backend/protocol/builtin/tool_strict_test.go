package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func strictToolRequest(codec, strict string) string {
	option := ""
	if strict != "omitted" {
		option = `,"strict":` + strict
	}
	schema := `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`
	switch codec {
	case Chat:
		return fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":"hi"}],"store":false,"max_completion_tokens":100,"tools":[{"type":"function","function":{"name":"f","parameters":%s%s}}]}`, schema, option)
	case Responses:
		return fmt.Sprintf(`{"model":"m","input":"hi","store":false,"max_output_tokens":100,"tools":[{"type":"function","name":"f","parameters":%s%s}]}`, schema, option)
	default:
		return fmt.Sprintf(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f","input_schema":%s%s}]}`, schema, option)
	}
}

func TestToolStrictProjection(t *testing.T) {
	for _, sourceCodec := range []string{Chat, Responses, Anthropic} {
		for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
			for _, strict := range []string{"omitted", "null", "true", "false"} {
				for _, mode := range []string{"compatible", "strict"} {
					t.Run(strings.Join([]string{sourceCodec, targetCodec, strict, mode}, "/"), func(t *testing.T) {
						source, target := testCompiled(t, sourceCodec), testCompiled(t, targetCodec)
						req, err := source.DecodeRequest(t.Context(), []byte(strictToolRequest(sourceCodec, strict)), p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						before, _ := p.EncodeValue(req)
						policy := p.DefaultConversionPolicy(source, target)
						policy.Mode = mode
						conversion, err := p.ResolveConversion(policy)
						if err != nil {
							t.Fatal(err)
						}
						sink := &p.DiagnosticSink{}
						out, err := conversion.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
						absent := strict == "omitted" || strict == "null"
						loss := targetCodec == Gemini && !absent || sourceCodec == Responses && targetCodec != Responses && absent
						if mode == "strict" && loss {
							var failure *p.ConversionError
							if !errors.As(err, &failure) || !failure.CandidateOnly {
								t.Fatalf("expected candidate loss: %v", err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						if out.Tools[0].InputSchema != req.Tools[0].InputSchema {
							t.Fatal("tool schema changed")
						}
						wire, err := target.EncodeRequest(t.Context(), out, p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						want := strict
						if sourceCodec != targetCodec && absent {
							want = "omitted"
						}
						if absent && targetCodec == Responses && (sourceCodec == Chat || sourceCodec == Anthropic) {
							want = "false"
						}
						if targetCodec == Gemini {
							want = "omitted"
						}
						got := decoded.Tools[0].Options["strict"]
						if want == "omitted" {
							if !got.IsZero() {
								t.Fatalf("unexpected strict: %s", wire)
							}
						} else if string(got.Bytes()) != want {
							t.Fatalf("strict=%s want %s: %s", got.Bytes(), want, wire)
						}
						losses := 0
						for _, issue := range sink.Issues() {
							if issue.Fidelity == "lossy_compatible" {
								losses++
								if issue.RuleID != "request-projection" || issue.Severity != p.SeverityWarning {
									t.Fatal(issue)
								}
							}
						}
						if loss && losses != 1 || !loss && losses != 0 {
							t.Fatal("unexpected losses", sink.Issues())
						}
						after, _ := p.EncodeValue(req)
						if before != after {
							t.Fatal("source request changed")
						}
					})
				}
			}
		}
	}
}

func TestToolStrictInvalidInput(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic} {
		for _, value := range []string{`"true"`, `1`, `[]`, `{}`} {
			t.Run(codec+"/"+value, func(t *testing.T) {
				source := testCompiled(t, codec)
				req, err := source.DecodeRequest(t.Context(), []byte(strictToolRequest(codec, value)), p.EvaluationContext{})
				if err == nil {
					conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, testCompiled(t, Responses)))
					_, err = conversion.Request(t.Context(), req, p.ConversionContext{}, nil)
				}
				if err == nil {
					t.Fatal("accepted invalid strict")
				}
				var failure *p.ConversionError
				if errors.As(err, &failure) && failure.CandidateOnly {
					t.Fatal("input error became candidate-only")
				}
			})
		}
	}
}
