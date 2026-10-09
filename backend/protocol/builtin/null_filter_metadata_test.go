package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesNullFiltersUseMetadataRuleInJSONAndSSE(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, codec := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict"} {
			for _, enabled := range []bool{true, false} {
				t.Run(codec+"/"+mode+"/"+map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
					policy := p.DefaultConversionPolicy(to, from)
					policy.Mode = mode
					for i := range policy.Rules {
						if policy.Rules[i].Action == "response_metadata" {
							policy.Rules[i].Enabled = enabled
						}
					}
					c, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					// Supply real required counts so a strict failure cannot be
					// mistaken for the separate Anthropic usage envelope policy.
					body := `{"id":"r","model":"m","status":"in_progress","output":[],"content_filters":null,"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}`
					response, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					for _, stream := range []bool{false, true} {
						sink := &p.DiagnosticSink{}
						if stream {
							frame, decodeErr := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":`+body+`}`), p.EvaluationContext{State: p.NewEvaluationState()})
							if decodeErr != nil {
								t.Fatal(decodeErr)
							}
							options := p.EvaluationContext{State: p.NewEvaluationState()}
							for _, event := range frame.Events {
								projected, e := c.Event(t.Context(), event, p.ConversionContext{}, sink)
								if e != nil {
									t.Fatal(e)
								}
								_, err = to.EncodeFrames(t.Context(), projected, options)
								if err != nil {
									break
								}
							}
						} else {
							projected, e := c.Response(t.Context(), response, p.ConversionContext{}, sink)
							if e != nil {
								t.Fatal(e)
							}
							_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
						}
						if enabled {
							if err != nil {
								t.Fatal(err)
							}
							found := false
							for _, issue := range sink.Issues() {
								if strings.HasSuffix(issue.Path, "/content_filters") {
									found = true
									if issue.Code != p.ConversionNormalized || issue.Severity != p.SeverityInfo || issue.Fidelity != "preserved" || issue.RuleID == "" || issue.PolicyHash != c.Hash {
										t.Fatal(issue)
									}
								}
							}
							if !found {
								t.Fatal("null filtering sentinel omitted without rule diagnostic")
							}
						} else if err == nil || !strings.Contains(err.Error(), "content_filters") {
							t.Fatalf("disabled metadata rule still normalized: %v", err)
						}
					}
				})
			}
		}
	}
}

func TestResponsesNonNullFiltersRemainUnknown(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, value := range []string{`null`, `{}`, `[]`, `false`, `42`, `{"blocked":true}`} {
		body := `{"id":"r","model":"m","output":[],"content_filters":` + value + `}`
		r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
		wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil || !strings.Contains(string(wire), `"content_filters":`+value) {
			t.Fatalf("native field changed: %s %v", wire, err)
		}
		projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		if (err == nil) != (value == `null`) {
			t.Fatalf("unexpected projection of %s: %v", value, err)
		}
		frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":`+body+`}`), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		for _, event := range frame.Events {
			projected, e := c.Event(t.Context(), event, p.ConversionContext{}, nil)
			if e != nil {
				t.Fatal(e)
			}
			_, err = to.EncodeFrames(t.Context(), projected, options)
			if err != nil {
				break
			}
		}
		if (err == nil) != (value == `null`) {
			t.Fatalf("unexpected streaming projection of %s: %v", value, err)
		}
	}
	if err := p.ValidateMetadataValue(Responses, "response", "content_filters", testValue(t, `{}`), "/content_filters"); err == nil {
		t.Fatal("custom semantic mapping expanded null-only recognition")
	}
}
