package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestEmptyResponseStateMetadataIsNotActiveContext(t *testing.T) {
	for _, source := range []string{Anthropic, Responses} {
		from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, Chat)
		for _, state := range []string{`null`, `{}`, `{"applied_edits":[]}`, `{"applied_edits":[{"type":"clear_tool_uses_20250919"}]}`, `{"applied_edits":[],"unknown":true}`} {
			field := "context_management"
			if source == Responses {
				field = "moderation"
			}
			wire := strings.Replace(auditResponses[source], `{`, `{"`+field+`":`+state+`,`, 1)
			response, err := from.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			sink := &p.DiagnosticSink{}
			_, err = c.Response(t.Context(), response, p.ConversionContext{}, sink)
			wantOK := state == `null` || state == `{}` || source == Anthropic && state == `{"applied_edits":[]}`
			if (err == nil) != wantOK {
				t.Fatalf("%s %s: %v", source, state, err)
			}
			if wantOK && !sinkHas(sink, "/"+field) {
				t.Fatal("normalization not diagnosed")
			}
			encoded, err := from.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil || !strings.Contains(string(encoded), `"`+field+`":`+state) {
				t.Fatalf("same-wire state changed: %s %v", encoded, err)
			}
		}
	}
}

func TestAnthropicNullStopDetailsJSONAndSSE(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Chat)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	wire := strings.Replace(auditResponses[Anthropic], `{`, `{"stop_details":null,`, 1)
	r, err := from.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	r, err = c.Response(t.Context(), r, p.ConversionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = to.EncodeResponse(t.Context(), r, p.EvaluationContext{}); err != nil {
		t.Fatal(err)
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	for _, frame := range []string{
		`{"type":"message_start","message":{"id":"r","type":"message","model":"m","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"output_tokens":0}}`,
	} {
		events, err := from.DecodeFrame(t.Context(), testValue(t, frame), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events.Events {
			if event.Unmapped != nil {
				t.Fatal("known null state captured as unknown")
			}
			if _, err = c.Event(t.Context(), event, p.ConversionContext{}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
}
