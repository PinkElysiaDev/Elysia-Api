package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicStreamResponseIDExtension(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	defer options.State.Release()
	source, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	target, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	for _, raw := range []string{
		`{"type":"message_start","response_id":{"extension":true},"message":{"id":"actual","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"response_id":"extension","content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"response_id":null,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0,"response_id":7}`,
		`{"type":"message_delta","response_id":["opaque"],"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop","response_id":false}`,
	} {
		frame := testValue(t, raw)
		decoded, err := source.DecodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal("extension replaced the source identity", err)
		}
		for _, event := range decoded.Events {
			if !event.ResponseID.IsZero() && !event.ResponseID.IsString("actual") {
				t.Fatal("extension became the core response identity", event.ResponseID)
			}
		}
		if err := target.Consume(t.Context(), frame); err != nil {
			t.Fatal("extension changed the final wire identity", err)
		}
	}
	for _, validation := range []*p.WireStreamValidation{source, target} {
		if err := validation.Finish(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnthropicStreamRequiresDeclaredBlockIndex(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	start := testValue(t, `{"type":"message_start","message":{"id":"response","model":"model","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
	for _, raw := range []string{
		`{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_start","index":null,"content_block":{"type":"text","text":""}}`,
	} {
		validator, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.Consume(t.Context(), start); err != nil {
			t.Fatal(err)
		}
		err = validator.Consume(t.Context(), testValue(t, raw))
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != "/index" {
			t.Fatal("missing or null block index acquired index zero", err)
		}
	}
}
