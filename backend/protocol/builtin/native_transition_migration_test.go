package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestLegacyNativeStreamTransitionFailure(t *testing.T) {
	before := checkNativeStreamTransitionFailure(t, shippedProjectionProtocol(t, Anthropic))
	after := checkNativeStreamTransitionFailure(t, compileBuiltinPackage(t, "anthropic"))
	checkPackageFailureRejection(t, before, after)
}

func checkNativeStreamTransitionFailure(t *testing.T, compiled *p.Compiled) error {
	t.Helper()
	source := p.EvaluationContext{State: p.NewEvaluationState()}
	target := p.EvaluationContext{State: p.NewEvaluationState()}
	defer source.State.Release()
	defer target.State.Release()
	frames := []string{
		`{"type":"message_start","message":{"id":"r","model":"m","usage":{"input_tokens":0,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"original"}}`,
	}
	for index, raw := range frames {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), source)
		if err != nil {
			t.Fatal(err)
		}
		if index < len(frames)-1 {
			output, err := compiled.EncodeFrame(t.Context(), frame, target)
			if err != nil || len(output) != 1 || output[0].JSONString() != raw {
				t.Fatal("legacy native prefix failed", output, err)
			}
			continue
		}
		events := append([]p.Event(nil), frame.Events...)
		events[0].Delta = p.StringValue("edited")
		_, err = compiled.EncodeConvertedFrame(t.Context(), frame, events, target)
		if err == nil || !strings.Contains(err.Error(), "target item has no start") {
			t.Fatal("recorded legacy transition failure changed", err)
		}
		return err
	}
	t.Fatal("transition fixture did not reach its edited frame")
	return nil
}
