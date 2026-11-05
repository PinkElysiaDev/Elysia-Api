package builtin

import (
	"fmt"
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestLegacyNativeStreamTransitionFailure(t *testing.T) {
	before := checkNativeStreamTransitionFailure(t, shippedProjectionProtocol(t, Anthropic))
	after := checkNativeStreamTransitionFailure(t, compileBuiltinPackage(t, "anthropic"))
	checkPackageFailureRejection(t, before, after)
}

// An independent encoder keeps the legacy rejection above. Only a target bound
// to its delivered wire history can resume a modified text delta.
func TestAnthropicPackageNativeTextTransition(t *testing.T) {
	const stateItemLimit = 64
	limits := p.DefaultLimits()
	limits.StateItems = stateItemLimit
	spec, err := os.ReadFile("packages/anthropic/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("packages/anthropic/mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	// This test bounds live stream state; activation fixture growth is tested
	// independently with the shipped package's normal limits.
	value, err := p.ParseValue(mapping)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := value.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"samples", "modelSamples", "tokenCountSamples"} {
		delete(fields, key)
	}
	value, err = p.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	mapping = value.Bytes()
	compiler, err := p.NewCompiler(limits, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(spec, mapping)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name           string
		isMixed        bool
		generatedIndex int
		blockCount     int
	}{
		{"native-only", false, -1, stateItemLimit},
		{"native-prefix-new-item", false, 1, 1},
		{"mixed-text", true, 8, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := p.EvaluationContext{State: p.NewEvaluationState()}
			target := source
			defer source.State.Release()
			wire, err := compiled.NewWireStreamValidation(target.Scope)
			if err != nil {
				t.Fatal(err)
			}
			target = target.WithOutputStream(wire)
			consume := func(batch []p.Value, err error) []p.Value {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range batch {
					if err := wire.Consume(t.Context(), value); err != nil {
						t.Fatal(err, value.JSONString())
					}
				}
				if len(batch) > 0 {
					if err := wire.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
				}
				return batch
			}
			decode := func(raw string) *p.EventFrame {
				t.Helper()
				frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), source)
				if err != nil {
					t.Fatal(err)
				}
				return frame
			}
			replay := func(raw string) {
				t.Helper()
				batch := consume(compiled.EncodeFrame(t.Context(), decode(raw), target))
				if len(batch) != 1 || batch[0].JSONString() != raw {
					t.Fatal("native frame was changed or replayed more than once", batch)
				}
			}
			replay(`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`)
			replay(`{ "type":"content_block_start","index":7,"content_block":{"type":"text","text":"\u0070refix"}}`)
			const delta = `{"type":"content_block_delta","index":7,"delta":{"type":"text_delta","text":"original"}}`
			if fixture.isMixed {
				frame := decode(delta)
				if len(frame.Events) != 1 {
					t.Fatal("text fixture must decode to one event", frame.Events)
				}
				events := append([]p.Event(nil), frame.Events...)
				events[0].Delta = p.StringValue("edited")
				batch := consume(compiled.EncodeConvertedFrame(t.Context(), frame, events, target))
				checkAnthropicTextFrame(t, batch, "content_block_delta", 7, "edited")
				replay(`{"type":"content_block_delta","index":7,"delta":{"type":"text_delta","text":"native"}}`)

				snapshot := events[0]
				snapshot.Type, snapshot.Delta = p.ItemSnapshot, p.Value{}
				snapshot.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("prefixeditednative")}
				if batch := consume(compiled.EncodeFrames(t.Context(), snapshot, target)); len(batch) != 0 {
					t.Fatal("already replayed inline text was emitted again", batch)
				}
				snapshot.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("prefixeditednative!")}
				batch = consume(compiled.EncodeFrames(t.Context(), snapshot, target))
				checkAnthropicTextFrame(t, batch, "content_block_delta", 7, "!")
			} else {
				replay(delta)
			}
			if fixture.generatedIndex >= 0 {
				generated := p.Event{SchemaVersion: compiled.CoreVersion(), Type: p.ItemStarted,
					ResponseID: p.StringValue("r"), ItemID: p.StringValue("generated"),
					Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("")}}
				batch := consume(compiled.EncodeFrames(t.Context(), generated, target))
				checkAnthropicTextFrame(t, batch, "content_block_start", fixture.generatedIndex, "")
				generated.Type, generated.Item = p.ItemFinished, nil
				batch = consume(compiled.EncodeFrames(t.Context(), generated, target))
				checkAnthropicTextFrame(t, batch, "content_block_stop", fixture.generatedIndex, "")
			}
			replay(`{"type":"content_block_stop","index":7}`)
			for block := 1; block < fixture.blockCount; block++ {
				index := 7 + block
				replay(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":"prefix"}}`, index))
				replay(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":"delta"}}`, index))
				replay(fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
			}
			replay(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
			replay(`{"type":"message_stop"}`)
			if tail := consume(compiled.FinishEvents(t.Context(), target)); len(tail) != 0 {
				t.Fatal("native terminal was emitted twice", tail)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkAnthropicTextFrame(t *testing.T, batch []p.Value, kind string, index int, text string) {
	t.Helper()
	if len(batch) != 1 {
		t.Fatalf("want one %s frame, got %v", kind, batch)
	}
	var frame struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := batch[0].Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != kind || frame.Index != index || frame.Delta.Text != text {
		t.Fatalf("unexpected text frame: %s", batch[0].JSONString())
	}
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
