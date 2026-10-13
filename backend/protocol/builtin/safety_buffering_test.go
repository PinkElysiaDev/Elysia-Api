package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesSafetyBufferingProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict"} {
			for _, enabled := range []bool{true, false} {
				for _, raw := range []string{"false", "true"} {
					policy := p.DefaultConversionPolicy(to, from)
					policy.Mode = mode
					for i := range policy.Rules {
						if policy.Rules[i].Action == "response_metadata" {
							policy.Rules[i].Enabled = enabled
						}
					}
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					fields, _ := liveAuditFixture(t, "safety_buffering").ReadObject()
					fields["safety_buffering"] = testValue(t, raw)
					frame, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()})
					if err != nil {
						t.Fatal(err)
					}
					if len(frame.Events) != 1 {
						t.Fatal(frame.Events)
					}
					// Isolate this metadata rule from timestamp and usage losses.
					frame.Events[0].Response.Attributes = nil
					frame.Events[0].Response.Usage = &p.Usage{Input: &p.Counter{Count: 2, Origin: p.ObservedCount}, Output: &p.Counter{Count: 0, Origin: p.ObservedCount}}
					sink := &p.DiagnosticSink{}
					event, err := conversion.Event(t.Context(), frame.Events[0], p.ConversionContext{}, sink)
					var frames []p.Value
					if err == nil {
						frames, err = to.EncodeFrames(t.Context(), event, p.EvaluationContext{State: p.NewEvaluationState()})
					}
					wantOK := target == Responses || enabled && raw == "false"
					if (err == nil) != wantOK {
						t.Fatalf("%s %s enabled=%v %s: %v", target, mode, enabled, raw, err)
					}
					if !wantOK {
						if !strings.Contains(err.Error(), "/safety_buffering") {
							t.Fatal(err)
						}
						continue
					}
					if target == Responses {
						if len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), `"safety_buffering":`+raw) {
							t.Fatal(frames)
						}
					} else {
						found := false
						for _, issue := range sink.Issues() {
							if issue.Path == "/safety_buffering" {
								found = issue.Code == p.ConversionNormalized && issue.Fidelity == "preserved" && issue.RuleID != "" && issue.PolicyHash == conversion.Hash && issue.Stage == "conversion.event" && issue.Direction == p.EncodeEvent
							}
						}
						if !found {
							t.Fatal("missing normalization evidence", sink.Issues())
						}
					}
				}
			}
		}
		frames := responsesPaddedTextFrames()
		frames[0] = strings.Replace(frames[0], `"type":"response.created"`, `"type":"response.created","safety_buffering":false`, 1)
		if len(auditConvertFrames(t, Responses, target, frames)) == 0 {
			t.Fatal("complete stream disappeared")
		}
	}
}

func TestResponsesSafetyBufferingTypeAndLocation(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, raw := range []string{`null`, `42`, `"false"`, `{}`, `[]`} {
		fields, _ := liveAuditFixture(t, "safety_buffering").ReadObject()
		fields["safety_buffering"] = testValue(t, raw)
		_, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()})
		if err == nil || !strings.Contains(err.Error(), "/safety_buffering") {
			t.Fatal(raw, err)
		}
	}
	for _, location := range []string{"response", "event:response.completed", "event:keepalive"} {
		if p.MetadataFieldType(Responses, location, "safety_buffering") != "" {
			t.Fatal(location)
		}
	}
}
