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
						if policy.Rules[i].ID == "response-metadata" || policy.Rules[i].ID == "event-metadata" || policy.Rules[i].ID == "history-response-metadata" {
							policy.Rules[i].Enabled = enabled
						}
					}
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					fields, _ := liveAuditFixture(t, "safety_buffering").ReadObject()
					fields["safety_buffering"] = testValue(t, raw)
					frame, err := from.DecodeFrame(t.Context(), object(fields), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
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
					route, err := conversion.WithFrameUsage(fixtureConversionContext(t, conversion, p.ConversionEvent, frame.Events[0].Source), frame.Events)
					if err != nil {
						t.Fatal(err)
					}
					event, err := conversion.Event(t.Context(), frame.Events[0], route, sink)
					var frames []p.Value
					if err == nil {
						options, consume := bindFixtureOutput(t, to, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
						frames, err = to.EncodeFrames(t.Context(), event, options.WithConversion(conversion, route))
						if err == nil {
							consume(frames)
						}
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
		_, err := from.DecodeFrame(t.Context(), object(fields), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
		if err == nil || !strings.Contains(err.Error(), "/safety_buffering") {
			t.Fatal(raw, err)
		}
	}
	response, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","created_at":1,"output":[],"safety_buffering":false}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range response.Metadata {
		if metadata.Name == "safety_buffering" {
			t.Fatal("response-level extension acquired event semantics", metadata)
		}
	}
	for _, sample := range []struct {
		prefix []string
		frame  string
	}{
		{nil, `{"type":"response.failed","response":{"id":"r","model":"m","output":[],"status":"failed","error":{"code":"server_error","message":"failed"}},"safety_buffering":false}`},
		{nil, `{"type":"keepalive","safety_buffering":false}`},
		{toolStreams[Responses][:2], `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"r_item_0","delta":"{}","safety_buffering":false}`},
	} {
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		t.Cleanup(options.State.Release)
		for _, raw := range sample.prefix {
			if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), options); err != nil {
				t.Fatal(err)
			}
		}
		frame, err := from.DecodeFrame(t.Context(), testValue(t, sample.frame), options)
		if err != nil {
			t.Fatal(err)
		}
		hasUnmapped := false
		for _, event := range frame.Events {
			hasUnmapped = hasUnmapped || event.Unmapped != nil
			for _, metadata := range event.Metadata {
				if metadata.Name == "safety_buffering" {
					t.Fatal("unsupported event acquired buffering semantics", sample.frame)
				}
			}
		}
		if !hasUnmapped {
			t.Fatal("unknown buffering extension lost its guard", sample.frame)
		}
	}
}

func TestResponsesSafetyBufferingThroughoutStream(t *testing.T) {
	raw := responsesPaddedTextFrames()
	progress := strings.Replace(raw[0], "response.created", "response.in_progress", 1)
	raw = append([]string{raw[0], progress}, raw[1:]...)
	names := map[string]bool{"response.created": true, "response.in_progress": true, "response.output_text.delta": true, "response.completed": true}
	for index, body := range raw {
		fields, _ := testValue(t, body).ReadObject()
		var name string
		if err := fields["type"].Decode(&name); err != nil {
			t.Fatal(err)
		}
		if names[name] {
			fields["safety_buffering"] = testValue(t, "false")
		}
		delete(fields, "sequence_number")
		raw[index] = object(fields).JSONString()
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		count := 0
		for _, frame := range auditConvertFrames(t, Responses, target, raw) {
			if strings.Contains(frame.JSONString(), `"safety_buffering":false`) {
				count++
			}
		}
		if target == Responses && count < len(names) || target != Responses && count != 0 {
			t.Fatal("buffering observation changed owner", target, count)
		}
	}
	compiled := testCompiled(t, Responses)
	for _, body := range raw {
		fields, _ := testValue(t, body).ReadObject()
		if fields["safety_buffering"].IsZero() {
			continue
		}
		fields["sequence_number"] = testValue(t, "1")
		if fields["type"].IsString("response.in_progress") {
			response, _ := fields["response"].ReadObject()
			response["object"] = p.StringValue("response")
			fields["response"] = object(response)
		}
		for _, value := range []string{"true", "null", "42", `"false"`} {
			fields["safety_buffering"] = testValue(t, value)
			err := compiled.ValidateWireOutput(p.EncodeEvent, object(fields))
			if value == "true" && err != nil || value != "true" && (err == nil || !strings.Contains(err.Error(), "/safety_buffering")) {
				t.Fatal("buffering type contract changed", fields["type"], value, err)
			}
		}
	}
}
