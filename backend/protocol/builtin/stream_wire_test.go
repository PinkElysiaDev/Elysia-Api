package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesStreamRejectsChangedItemAssociation(t *testing.T) {
	for _, bad := range []string{
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"different","delta":"{}"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"original","delta":"{}"}`,
		`{"type":"response.output_item.done","output_index":0,"item_id":"original","item":{"id":"different","type":"function_call","call_id":"c","name":"f","arguments":"{}"}}`,
	} {
		compiled := testCompiled(t, Responses)
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.output_item.added","output_index":0,"item":{"id":"original","type":"function_call","call_id":"c","name":"f","arguments":""}}`), options); err != nil {
			t.Fatal(err)
		}
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, bad), options); err == nil {
			t.Fatal("changed item association accepted", bad)
		}
	}
}

// Inspect wire objects independently of the adapter decoder: accepting our own
// output does not prove that an upstream/client will accept its structure.
func TestResponsesStreamWireItemIdentity(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		frames []string
		kind   string
	}{{"text", textStreams[Chat], "message"}, {"tool", toolStreams[Chat], "function_call"}} {
		t.Run(fixture.name, func(t *testing.T) {
			frames := convertStream(t, Chat, Responses, fixture.frames)
			var itemID string
			var hasDone, hasTerminal bool
			for index, frame := range frames {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(frame.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				var kind string
				if err := json.Unmarshal(fields["type"], &kind); err != nil {
					t.Fatal(err)
				}
				var sequence int
				if err := json.Unmarshal(fields["sequence_number"], &sequence); err != nil || sequence != index {
					t.Fatalf("sequence %s: %v", frame.Bytes(), err)
				}
				if kind == "response.output_item.added" || kind == "response.output_item.done" {
					var item struct {
						ID, Type string
						Content  []map[string]json.RawMessage
					}
					if err := json.Unmarshal(fields["item"], &item); err != nil {
						t.Fatal(err)
					}
					if item.Type != fixture.kind || item.ID == "" {
						t.Fatalf("invalid item: %s", frame.Bytes())
					}
					if itemID == "" {
						itemID = item.ID
					}
					if item.ID != itemID {
						t.Fatalf("item ID changed: %s", frame.Bytes())
					}
					hasDone = hasDone || kind == "response.output_item.done"
				}
				if raw, exists := fields["item_id"]; exists {
					var id string
					if err := json.Unmarshal(raw, &id); err != nil || id != itemID {
						t.Fatalf("unassociated frame: %s", frame.Bytes())
					}
				}
				if kind == "response.completed" {
					var response struct {
						Output []struct {
							ID, Type string
							Content  []struct{ Type, Text string }
						}
					}
					if err := json.Unmarshal(fields["response"], &response); err != nil {
						t.Fatal(err)
					}
					if len(response.Output) != 1 || response.Output[0].ID != itemID || response.Output[0].Type != fixture.kind {
						t.Fatalf("invalid terminal output: %s", frame.Bytes())
					}
					if fixture.kind == "message" && (len(response.Output[0].Content) != 1 || response.Output[0].Content[0].Type != "output_text" || response.Output[0].Content[0].Text != "hi") {
						t.Fatalf("text outside message content: %s", frame.Bytes())
					}
					hasTerminal = true
				}
			}
			if !hasDone || !hasTerminal {
				t.Fatal("incomplete item lifecycle")
			}
		})
	}
}

func TestStreamWaitsForToolIdentity(t *testing.T) {
	for _, target := range []string{Chat, Anthropic, Responses, Gemini} {
		t.Run(target, func(t *testing.T) {
			compiled := testCompiled(t, target)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			start := p.Event{SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue("local"), Item: &p.Node{Kind: p.ToolCallNode, Input: &p.ToolInput{Kind: p.JSONInput}}}
			frames, err := compiled.EncodeFrames(t.Context(), start, options)
			if err != nil || len(frames) != 0 {
				t.Fatal("nameless start emitted", frames, err)
			}
			frames, err = compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: start.ItemID, Delta: p.StringValue(`{"n":`)}, options)
			if err != nil || len(frames) != 0 {
				t.Fatal("unassociated input emitted", frames, err)
			}
			start.Type = p.ItemSnapshot
			start.Item.Name, start.Item.CallID = p.StringValue("lookup"), p.StringValue("call")
			frames, err = compiled.EncodeFrames(t.Context(), start, options)
			if err != nil {
				t.Fatal(err)
			}
			delta, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: start.ItemID, Delta: p.StringValue(`9007199254740993}`)}, options)
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, delta...)
			end, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: 1, Type: p.ItemFinished, ItemID: start.ItemID}, options)
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, end...)
			var input strings.Builder
			for _, frame := range frames {
				fields, err := frame.ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				switch target {
				case Responses:
					if string(fields["type"].Bytes()) == `"response.function_call_arguments.delta"` {
						value, _ := stringValue(fields["delta"])
						input.WriteString(value)
					}
				case Anthropic:
					if string(fields["type"].Bytes()) == `"content_block_delta"` {
						update, _ := fields["delta"].ReadObject()
						value, _ := stringValue(update["partial_json"])
						input.WriteString(value)
					}
				case Chat:
					choices, _ := readArray(fields["choices"])
					choice, _ := choices[0].ReadObject()
					update, _ := choice["delta"].ReadObject()
					calls, _ := readArray(update["tool_calls"])
					call, _ := calls[0].ReadObject()
					function, _ := call["function"].ReadObject()
					value, _ := stringValue(function["arguments"])
					input.WriteString(value)
				case Gemini:
					candidates, _ := readArray(fields["candidates"])
					candidate, _ := candidates[0].ReadObject()
					content, _ := candidate["content"].ReadObject()
					parts, _ := readArray(content["parts"])
					part, _ := parts[0].ReadObject()
					call, _ := part["functionCall"].ReadObject()
					input.Write(call["args"].Bytes())
				}
			}
			if input.String() != `{"n":9007199254740993}` {
				t.Fatalf("input repeated or lost: %s", input.String())
			}
		})
	}
}
