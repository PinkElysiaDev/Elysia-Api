package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesSentinelHeartbeatIsTransportOnly(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	heartbeat := liveAuditFixture(t, "heartbeat")
	options := p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: &p.DiagnosticSink{}}
	for range 3 {
		frame, err := from.DecodeFrame(t.Context(), heartbeat, options)
		if err != nil || len(frame.Events) != 0 {
			t.Fatal(frame, err)
		}
		for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
			out, err := shippedProjectionProtocol(t, target).EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()})
			if err != nil || len(out) != 0 {
				t.Fatal("heartbeat replayed", target, out, err)
			}
		}
	}
	if len(options.Diagnostics.Issues()) != 1 {
		t.Fatal(options.Diagnostics.Issues())
	}
	if events, err := from.DecodeEvents(t.Context(), heartbeat, p.EvaluationContext{State: p.NewEvaluationState()}); err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		frames := responsesPaddedTextFrames()
		frames = append([]string{string(heartbeat.Bytes())}, frames...)
		frames = append(frames[:4], append([]string{string(heartbeat.Bytes())}, frames[4:]...)...)
		auditConvertFrames(t, Responses, target, frames)
	}
	replay, err := p.NewEventReplay(p.Target{Protocol: from.Identity(), Direction: p.EncodeEvent, Capabilities: from.Capabilities(p.EncodeEvent)}, from.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = replay.Finish(); err == nil {
		t.Fatal("only heartbeat became a successful generation")
	}
}

func TestResponsesSentinelHeartbeatRejectsPayload(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for key, values := range map[string][]string{
		"delta": {`"real text"`, `null`}, "item_id": {`"message_real"`},
		"SSE-Keep-Alive": {`false`, `null`, `"true"`, `1`},
		"output_index":   {`1`, `null`, `"0"`}, "content_index": {`1`},
		"vendor": {`42`}, "sequence_number": {`null`, `"1"`},
	} {
		for _, value := range values {
			fields, _ := liveAuditFixture(t, "heartbeat").ReadObject()
			fields[key] = testValue(t, value)
			if _, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
				t.Fatal(key, value)
			}
		}
	}
	fields, _ := liveAuditFixture(t, "heartbeat").ReadObject()
	delete(fields, "SSE-Keep-Alive")
	if _, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil || !strings.Contains(err.Error(), "no start") {
		t.Fatal("unmarked delta hidden", err)
	}
}
