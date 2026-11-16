package builtin

import (
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
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`), options); err != nil {
			t.Fatal(err)
		}
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
		kinds  []string
		texts  []string
	}{{"text", textStreams[Chat], []string{"message", "message"}, []string{"", "hi"}}, {"tool", toolStreams[Chat], []string{"message", "function_call"}, []string{"", ""}}} {
		t.Run(fixture.name, func(t *testing.T) {
			frames := convertStream(t, Chat, Responses, fixture.frames)
			assertResponseStreamIdentities(t, frames, fixture.kinds, fixture.texts)
		})
	}
}

type responseIdentityItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Content   []struct{ Type, Text string }
}

func assertResponseStreamIdentities(t *testing.T, frames []p.Value, kinds, texts []string) {
	t.Helper()
	identities := make(map[int]string, len(kinds))
	completed := make(map[int]bool, len(kinds))
	hasTerminal := false
	for index, frame := range frames {
		var envelope struct {
			Type           string               `json:"type"`
			SequenceNumber *int                 `json:"sequence_number"`
			OutputIndex    int                  `json:"output_index"`
			ItemID         string               `json:"item_id"`
			Item           responseIdentityItem `json:"item"`
			Response       struct{ Output []responseIdentityItem }
		}
		if err := frame.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.SequenceNumber == nil || *envelope.SequenceNumber != index {
			t.Fatalf("sequence changed: %s", frame.Bytes())
		}
		if envelope.Type == "response.output_item.added" || envelope.Type == "response.output_item.done" {
			if envelope.OutputIndex >= len(kinds) || envelope.Item.Type != kinds[envelope.OutputIndex] || envelope.Item.ID == "" {
				t.Fatalf("invalid item: %s", frame.Bytes())
			}
			if envelope.Type == "response.output_item.added" {
				if identities[envelope.OutputIndex] != "" {
					t.Fatalf("item started twice: %s", frame.Bytes())
				}
				identities[envelope.OutputIndex] = envelope.Item.ID
			} else {
				if envelope.Item.ID != identities[envelope.OutputIndex] || completed[envelope.OutputIndex] {
					t.Fatalf("item completion changed identity: %s", frame.Bytes())
				}
				completed[envelope.OutputIndex] = true
			}
		}
		if envelope.ItemID != "" && envelope.ItemID != identities[envelope.OutputIndex] {
			t.Fatalf("unassociated frame: %s", frame.Bytes())
		}
		if envelope.Type != "response.completed" {
			continue
		}
		if hasTerminal || len(envelope.Response.Output) != len(kinds) {
			t.Fatalf("invalid terminal output: %s", frame.Bytes())
		}
		for outputIndex, item := range envelope.Response.Output {
			if item.ID != identities[outputIndex] || item.Type != kinds[outputIndex] || !completed[outputIndex] {
				t.Fatalf("terminal item lost its identity: %s", frame.Bytes())
			}
			if item.Type == "function_call" && (item.CallID != "call" || item.Arguments != `{"n":9007199254740993}`) {
				t.Fatalf("tool association or argument precision changed: %s", frame.Bytes())
			}
			if item.Type != "message" {
				continue
			}
			text := texts[outputIndex]
			if text == "" && len(item.Content) != 0 || text != "" && (len(item.Content) != 1 || item.Content[0].Type != "output_text" || item.Content[0].Text != text) {
				t.Fatalf("message content changed: %s", frame.Bytes())
			}
		}
		hasTerminal = true
	}
	if !hasTerminal || len(completed) != len(kinds) {
		t.Fatal("incomplete item lifecycle")
	}
}

func TestStreamWaitsForToolIdentity(t *testing.T) {
	for _, target := range []string{Chat, Anthropic, Responses, Gemini} {
		t.Run(target, func(t *testing.T) {
			compiled := testCompiled(t, target)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			options, consume := bindFixtureOutput(t, compiled, options)
			opening := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Response: &p.Response{
				SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"),
				Attributes: map[string]p.Value{"created_at": testValue(t, "1")},
				Usage:      &p.Usage{Input: &p.Counter{Count: 1, Origin: p.ObservedCount}, Output: &p.Counter{Count: 0, Origin: p.ObservedCount}},
			}}
			if target == Anthropic || target == Gemini {
				opening.Response.Attributes = nil
			}
			frames, err := compiled.EncodeFrames(t.Context(), opening, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(frames)
			start := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("local"), Item: &p.Node{Kind: p.ToolCallNode, Input: &p.ToolInput{Kind: p.JSONInput}}}
			if target == Responses {
				start.Item.ID = p.StringValue("tool")
			}
			frames, err = compiled.EncodeFrames(t.Context(), start, options)
			if err != nil || len(frames) != 0 {
				t.Fatal("nameless start emitted", frames, err)
			}
			frames, err = compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: start.ItemID, Delta: p.StringValue(`{"n":`)}, options)
			if err != nil || len(frames) != 0 {
				t.Fatal("unassociated input emitted", frames, err)
			}
			start.Type = p.ItemSnapshot
			start.Item.Name, start.Item.CallID = p.StringValue("lookup"), p.StringValue("call")
			frames, err = compiled.EncodeFrames(t.Context(), start, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(frames)
			delta, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: start.ItemID, Delta: p.StringValue(`9007199254740993}`)}, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(delta)
			frames = append(frames, delta...)
			end, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: start.ItemID}, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(end)
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
					if call["index"].JSONString() != "0" {
						t.Fatal("generated tool index is not dense", frame)
					}
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
