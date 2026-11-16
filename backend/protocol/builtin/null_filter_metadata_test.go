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
						if policy.Rules[i].ID == "response-metadata" || policy.Rules[i].ID == "event-metadata" || policy.Rules[i].ID == "history-response-metadata" {
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
					response, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					for _, stream := range []bool{false, true} {
						sink := &p.DiagnosticSink{}
						if stream {
							frame, decodeErr := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":`+body+`}`), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
							if decodeErr != nil {
								t.Fatal(decodeErr)
							}
							route, routeErr := c.WithFrameUsage(fixtureConversionContext(t, c, p.ConversionEvent, from.Identity()), frame.Events)
							if routeErr != nil {
								t.Fatal(routeErr)
							}
							options, consume := bindFixtureOutput(t, to, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
							for _, event := range frame.Events {
								projected, e := c.Event(t.Context(), event, route, sink)
								if e != nil {
									t.Fatal(e)
								}
								var frames []p.Value
								frames, err = to.EncodeFrames(t.Context(), projected, options.WithConversion(c, route))
								if err != nil {
									break
								}
								consume(frames)
							}
						} else {
							projected, e := c.Response(t.Context(), response, fixtureConversionContext(t, c, p.ConversionResponse, response.Source), sink)
							if e != nil {
								t.Fatal(e)
							}
							_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
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
		r, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
		for _, metadata := range r.Metadata {
			if metadata.Name == "content_filters" && (value != `null` || metadata.Meaning != "empty") {
				t.Fatal("source mapping expanded null-only recognition", value, metadata)
			}
		}
		wire, err := from.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
		if err != nil || !strings.Contains(string(wire), `"content_filters":`+value) {
			t.Fatalf("native field changed: %s %v", wire, err)
		}
		projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		if (err == nil) != (value == `null`) {
			t.Fatalf("unexpected projection of %s: %v", value, err)
		}
		frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":`+body+`}`), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
		if err != nil {
			t.Fatal(err)
		}
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		for _, event := range frame.Events {
			projected, e := c.Event(t.Context(), event, fixtureConversionContext(t, c, p.ConversionEvent, event.Source), nil)
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
}
