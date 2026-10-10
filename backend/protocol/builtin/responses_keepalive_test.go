package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestResponsesKeepaliveHasNoGenerationPayload(t *testing.T) {
	frames := responsesPaddedTextFrames()
	frames = append(frames[:1], append([]string{`{"type":"keepalive","sequence_number":1}`}, frames[1:]...)...)
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			wire := auditConvertFrames(t, Responses, target, frames)
			if len(wire) == 0 {
				t.Fatal("generation disappeared")
			}
			var text strings.Builder
			for _, value := range wire {
				text.Write(value.Bytes())
			}
			if !strings.Contains(text.String(), `"O"`) && !strings.Contains(text.String(), `"OK"`) {
				t.Fatal("body missing")
			}
		})
	}
}

func TestResponsesKeepaliveDoesNotHidePayloadOrBadSequence(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
	frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"keepalive","sequence_number":2}`), opts)
	if err != nil || len(frame.Events) != 0 {
		t.Fatal(frame, err)
	}
	encoded, err := from.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil || len(encoded) != 1 || encoded[0] != testValue(t, `{"type":"keepalive","sequence_number":2}`) {
		t.Fatal("native heartbeat changed", encoded, err)
	}
	if _, err = from.DecodeFrame(t.Context(), testValue(t, `{"type":"keepalive","sequence_number":1}`), opts); err == nil {
		t.Fatal("out-of-order keepalive accepted")
	}
	for _, raw := range []string{`{"type":"keepalive","sequence_number":"1"}`, `{"type":"keepalive","sequence_number":null}`} {
		if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
			t.Fatal("bad sequence accepted", raw)
		}
	}
	for _, field := range []string{`"delta":"private"`, `"response":{"id":"r"}`, `"vendor":42`, `"service_tier":"default"`} {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"keepalive",`+field+`}`), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			continue
		}
		if len(frame.Events) != 1 || frame.Events[0].Unmapped == nil {
			t.Fatal("undeclared keepalive payload lost", field, frame)
		}
	}
}
