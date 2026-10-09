package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestAnthropicDirectToolCallerIsClientCall(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	for _, caller := range []string{`{"type":"direct"}`, `{"type":"code_execution_20260120","tool_id":"server_call"}`, `{"type":"direct","vendor":true}`, `42`, `null`, `{"type":42}`} {
		block := `{"type":"tool_use","id":"c","name":"echo","input":{},"caller":` + caller + `}`
		body := `{"id":"r","model":"m","type":"message","role":"assistant","content":[` + block + `],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`
		r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		invalid := caller == `42` || caller == `null` || caller == `{"type":42}`
		if invalid {
			if err == nil || !strings.Contains(err.Error(), "/caller") {
				t.Fatal("invalid caller accepted", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		native, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, native, body)
		for _, target := range []string{Chat, Responses, Gemini} {
			to := shippedProjectionProtocol(t, target)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			}
			if (err == nil) != (caller == `{"type":"direct"}`) {
				t.Fatal(target, caller, err)
			}
			if caller == `{"type":"direct"}` {
				frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"type":"content_block_start","index":0,"content_block":`+block+`}`), p.EvaluationContext{State: p.NewEvaluationState()})
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
