package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicToolStreamPassesResponsesFinalValidation(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Delivery: &p.DeliveryState{ID: p.StringValue("r"), Created: testValue(t, `1`)}}
	validator, err := to.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"echo","caller":{"type":"direct"},"input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{ \"value\": 7 }"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	} {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, wire), options)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range frame.Events {
			frame.Events[i], err = c.Event(t.Context(), e, route, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		frames, err := to.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options)
		if err != nil {
			t.Fatal(wire, err)
		}
		for _, f := range frames {
			if err := validator.Consume(t.Context(), f); err != nil {
				t.Fatal(string(f.Bytes()), err)
			}
		}
	}
	frames, err := to.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if err := validator.Consume(t.Context(), f); err != nil {
			t.Fatal(string(f.Bytes()), err)
		}
	}
}
