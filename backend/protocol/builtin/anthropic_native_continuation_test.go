package builtin

import (
	"os"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageMultipleThinkingToolBlocks(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	raw, err := os.ReadFile("testdata/anthropic-stream-layout.json")
	if err != nil {
		t.Fatal(err)
	}
	var values []p.Value
	if err := testValue(t, string(raw)).Decode(&values); err != nil {
		t.Fatal(err)
	}
	raws := make([]string, len(values))
	for i, value := range values {
		raws[i] = value.JSONString()
	}
	frames, _ := decodePackageStream(t, compiled, raws, scope)
	if native := encodePackageStream(t, compiled, frames, scope, true); !slices.Equal(native, raws) {
		t.Fatal("native compound sequence changed or duplicated frames")
	}
	for _, frame := range frames {
		for index := range frame.Events {
			frame.Events[index].Native = nil
		}
	}
	rebuilt := encodePackageStream(t, compiled, frames, scope, false)
	_, response := decodePackageStream(t, compiled, rebuilt, scope)
	items := messageLeaves(t, response)
	if len(items) != 5 {
		t.Fatal("lost ordered blocks", items)
	}
	for _, index := range []int{0, 3} {
		text, signature := "plan-a", "sig-a"
		if index == 3 {
			text, signature = "plan-b", "sig-b"
		}
		if items[index].Kind != p.ReasoningNode || !items[index].Payload.IsString(text) || len(items[index].Resources) != 1 || !items[index].Resources[0].ID.IsString(signature) || items[index].Resources[0].Scope != scope {
			t.Fatal("thinking was merged, relabeled or lost its resource owner", index, items[index])
		}
	}
	if !items[1].Payload.IsString("h") || !items[4].Payload.IsString("i") || items[2].Kind != p.ToolCallNode || !items[2].CallID.IsString("call") || !items[2].Name.IsString("lookup") || items[2].Input == nil {
		t.Fatal("text/tool block order or identity changed", items)
	}
	sameJSON(t, items[2].Input.Value.Bytes(), `{"n":9007199254740993}`)
	if !response.Status.IsString("completed") || response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 20 || response.Usage.Output == nil || response.Usage.Output.Count != 2 {
		t.Fatal("multiple blocks changed usage or terminal", response)
	}
}

func TestAnthropicPackageNativeToolAndThinkingContinuation(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	const start = `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`
	for _, fixture := range []struct {
		name   string
		frames []string
		edit   func(int, p.Event) p.Event
		check  func(*testing.T, *p.Response)
	}{
		{
			name: "tool",
			frames: []string{
				start,
				`{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{}}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"0"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":",\"b\":"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"0}"}}`,
				`{"type":"content_block_stop","index":7}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`,
				`{"type":"message_stop"}`,
			},
			edit: func(index int, event p.Event) p.Event {
				if index == 3 {
					event.Delta = p.StringValue("1")
				}
				if index == 5 {
					event.Type, event.Delta = p.ItemSnapshot, p.Value{}
					event.Item = &p.Node{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"a":1,"b":2}`)}}
				}
				return event
			},
			check: func(t *testing.T, response *p.Response) {
				item := messageLeaves(t, response)[0]
				if item.Kind != p.ToolCallNode || !item.CallID.IsString("call") || !item.Name.IsString("lookup") || item.Input == nil {
					t.Fatal("tool identity did not survive continuation", item)
				}
				sameJSON(t, item.Input.Value.Bytes(), `{"a":1,"b":2}`)
			},
		},
		{
			name: "thinking",
			frames: []string{
				start,
				`{"type":"content_block_start","index":7,"content_block":{"type":"thinking","thinking":"prefix","signature":"sig-"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"thinking_delta","thinking":"original"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"thinking_delta","thinking":"native"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"signature_delta","signature":"original"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"signature_delta","signature":"native"}}`,
				`{"type":"content_block_delta","index":7,"delta":{"type":"signature_delta","signature":"!"}}`,
				`{"type":"content_block_stop","index":7}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
				`{"type":"message_stop"}`,
			},
			edit: func(index int, event p.Event) p.Event {
				if index == 2 {
					event.Delta = p.StringValue("edited")
				}
				if index == 4 || index == 6 {
					signature := "sig-edited"
					if index == 6 {
						signature += "native!"
					}
					item := *event.Item
					item.Resources = []p.Resource{{Kind: "signature", ID: p.StringValue(signature), Scope: scope}}
					event.Item = &item
				}
				return event
			},
			check: func(t *testing.T, response *p.Response) {
				item := messageLeaves(t, response)[0]
				if item.Kind != p.ReasoningNode || !item.Payload.IsString("prefixeditednative") || len(item.Resources) != 1 || !item.Resources[0].ID.IsString("sig-editednative!") {
					t.Fatal("thinking or signature prefix was emitted again", item)
				}
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			frames, _ := decodePackageStream(t, compiled, fixture.frames, scope)
			wire, err := compiled.NewWireStreamValidation(scope)
			if err != nil {
				t.Fatal(err)
			}
			options := (p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}).WithOutputStream(wire)
			defer options.State.Release()
			var rendered []string
			consume := func(batch []p.Value, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				for _, frame := range batch {
					if err := wire.Consume(t.Context(), frame); err != nil {
						t.Fatal(frame.JSONString(), err)
					}
					if err := wire.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
					rendered = append(rendered, frame.JSONString())
				}
			}
			for index, frame := range frames {
				events := make([]p.Event, len(frame.Events))
				for position, event := range frame.Events {
					events[position] = fixture.edit(index, event)
				}
				consume(compiled.EncodeConvertedFrame(t.Context(), frame, events, options))
			}
			consume(compiled.FinishEvents(t.Context(), options))
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			_, response := decodePackageStream(t, compiled, rendered, scope)
			if len(messageLeaves(t, response)) != 1 || response.Usage == nil || response.Usage.Output == nil || response.Usage.Output.Count != 2 {
				t.Fatal("continuation changed item count or accounting", response)
			}
			fixture.check(t, response)
		})
	}
}
