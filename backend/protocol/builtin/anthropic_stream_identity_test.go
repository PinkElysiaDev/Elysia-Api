package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicStreamResponseIDExtension(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Anthropic), compileBuiltinPackage(t, "anthropic")
	oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
	newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
	defer oldOptions.State.Release()
	defer newOptions.State.Release()
	oldTarget, err := legacy.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := candidate.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := candidate.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"message_start","response_id":{"extension":true},"message":{"id":"actual","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"response_id":"extension","content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"response_id":null,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0,"response_id":7}`,
		`{"type":"message_delta","response_id":["opaque"],"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop","response_id":false}`,
	} {
		frame := testValue(t, raw)
		if _, err := legacy.DecodeFrame(t.Context(), frame, oldOptions); err != nil {
			t.Fatal("legacy source boundary", err)
		}
		if err := oldTarget.Consume(t.Context(), frame); err != nil {
			t.Fatal("legacy final wire boundary", err)
		}
		if err := source.Consume(t.Context(), frame); err != nil {
			t.Fatal("declared source identity", err)
		}
		if _, err := candidate.DecodeFrame(t.Context(), frame, newOptions); err != nil {
			t.Fatal("declared source mapping", err)
		}
		if err := target.Consume(t.Context(), frame); err != nil {
			t.Fatal("declared final wire identity", err)
		}
	}
	for _, validator := range []*p.WireStreamValidation{oldTarget, source, target} {
		if err := validator.Finish(); err != nil {
			t.Fatal(err)
		}
	}
}
