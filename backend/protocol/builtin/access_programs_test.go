package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesAccessProgramsProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, raw := range []string{`null`, `{"cyber":"standard"}`, `{"cyber":"daybreak_blue"}`, `{"cyber":"daybreak_red"}`} {
			for _, mode := range []string{"compatible", "strict"} {
				for _, enabled := range []bool{true, false} {
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
					body := `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"access_programs":` + raw + `}`
					for _, stream := range []bool{false, true} {
						sink := &p.DiagnosticSink{}
						var wire []byte
						if !stream {
							r, e := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
							if e != nil {
								t.Fatal(e)
							}
							r.Native = nil // Rebuilding must retain the native field too.
							r, err = c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), sink)
							if err == nil {
								wire, err = to.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
							}
						} else {
							frame, e := from.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":`+body+`}`), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
							if e != nil {
								t.Fatal(e)
							}
							options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
							options, consume := bindFixtureOutput(t, to, options)
							route := fixtureConversionContext(t, c, p.ConversionEvent, from.Identity())
							route, err = c.WithFrameUsage(route, frame.Events)
							if err != nil {
								t.Fatal(err)
							}
							options = options.WithConversion(c, route)
							for _, event := range frame.Events {
								event.Native = nil
								var v p.Event
								v, err = c.Event(t.Context(), event, route, sink)
								if err != nil {
									break
								}
								var frames []p.Value
								frames, err = to.EncodeFrames(t.Context(), v, options)
								if err == nil {
									consume(frames)
								}
								for _, f := range frames {
									wire = append(wire, f.Bytes()...)
								}
								if err != nil {
									break
								}
							}
						}
						wantOK := target == Responses || enabled && (mode == "compatible" || raw == `null`)
						if (err == nil) != wantOK {
							t.Fatalf("target=%s raw=%s mode=%s enabled=%v stream=%v: %v", target, raw, mode, enabled, stream, err)
						}
						if !wantOK {
							if !strings.Contains(err.Error(), "access_programs") {
								t.Fatal(err)
							}
							continue
						}
						if target == Responses {
							if !strings.Contains(string(wire), `"access_programs":`+raw) {
								t.Fatalf("native field lost: %s", wire)
							}
							continue
						}
						found := false
						for _, issue := range sink.Issues() {
							if strings.HasSuffix(issue.Path, "/access_programs") {
								found = true
								if issue.RuleID == "" || issue.PolicyHash != c.Hash {
									t.Fatal(issue)
								}
								if raw == `null` && (issue.Code != p.ConversionNormalized || issue.Fidelity != "preserved") {
									t.Fatal(issue)
								}
								if raw != `null` && issue.Fidelity != "lossy_compatible" {
									t.Fatal(issue)
								}
							}
						}
						if !found {
							t.Fatal("access program removed without diagnostic")
						}
					}
				}
			}
		}
	}
}

func TestResponsesAccessProgramsRejectUnknownAndInvalid(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, tc := range []struct{ value, path string }{
		{`42`, "/access_programs"}, {`[]`, "/access_programs"}, {`{}`, "/access_programs/cyber"},
		{`{"cyber":null}`, "/access_programs/cyber"}, {`{"cyber":"unknown"}`, "/access_programs/cyber"},
		{`{"cyber":"standard","extra":true}`, "/access_programs/extra"},
	} {
		_, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","output":[],"access_programs":`+tc.value+`}`), fixtureOptions(p.EvaluationContext{}))
		if err == nil || !strings.Contains(err.Error(), tc.path) {
			t.Fatalf("%s: %v", tc.value, err)
		}
		for _, eventType := range []string{"response.created", "response.in_progress", "response.completed"} {
			status := "in_progress"
			if eventType == "response.completed" {
				status = "completed"
			}
			frame := testValue(t, `{"type":"`+eventType+`","response":{"id":"r","status":"`+status+`","output":[],"access_programs":`+tc.value+`}}`)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			_, err := from.DecodeFrame(t.Context(), frame, options)
			options.State.Release()
			if err == nil || !strings.Contains(err.Error(), "/response"+tc.path) {
				t.Fatalf("%s %s: %v", eventType, tc.value, err)
			}
		}
	}
}
