package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicDirectToolCallerIsClientCall(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	for _, caller := range []string{`{"type":"direct"}`, `{"type":"code_execution_20260120","tool_id":"server_call"}`, `{"type":"direct","vendor":true}`, `42`, `null`, `{}`, `{"type":42}`} {
		block := `{"type":"tool_use","id":"c","name":"echo","input":{},"caller":` + caller + `}`
		body := `{"id":"r","model":"m","type":"message","role":"assistant","content":[` + block + `],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`
		r, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		isInvalid := caller == `42` || caller == `null` || caller == `{}` || caller == `{"type":42}`
		if isInvalid {
			if err == nil || !strings.Contains(err.Error(), "/caller") {
				t.Fatal("invalid caller accepted", err)
			}
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			_, err = from.DecodeFrame(t.Context(), testValue(t, `{"type":"content_block_start","index":0,"content_block":`+block+`}`), options)
			options.State.Release()
			if err == nil || !strings.Contains(err.Error(), "/content_block/caller") {
				t.Fatal("invalid stream caller accepted", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		native, err := from.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, native, body)
		for _, target := range []string{Chat, Responses, Gemini} {
			to := shippedProjectionProtocol(t, target)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), nil)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			}
			if (err == nil) != (caller == `{"type":"direct"}`) {
				t.Fatal(target, caller, err)
			}
			if caller == `{"type":"direct"}` {
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				defer options.State.Release()
				if _, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"message_start","message":{"id":"r","model":"m","content":[]}}`), options); err != nil {
					t.Fatal(err)
				}
				frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"content_block_start","index":0,"content_block":`+block+`}`), options)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range frame.Events {
					if e.Item != nil && len(e.Item.Attributes) != 0 {
						t.Fatal("direct caller left opaque attributes")
					}
				}
			}
		}
	}
}
