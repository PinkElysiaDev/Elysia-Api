package protocol

import (
	"fmt"
	"testing"
)

func TestBatchedLogprobsPreserveSequentialBytes(t *testing.T) {
	for _, codec := range []string{"openai-chat", "responses", "custom"} {
		spellings := []string{"null", `[]`, `[1e0,9007199254740993]`, `[ {"token":"<>&","token":"second","bytes":[1]} ]`, `{"content":null}`, `{"content":[],"refusal":[]}`, `{"content":[1],"content":[2],"unknown":[]}`, `{"content":[1e0],"refusal":["<>&"]}`, `{"content":"unsupported"}`, `"unsupported"`}
		values := make([]Value, len(spellings))
		for index, raw := range spellings {
			var err error
			values[index], err = ParseValue([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
		}
		values = append(values, Value{})
		for initialIndex, initial := range values {
			for firstIndex, first := range values {
				for secondIndex, second := range values {
					expected := appendLogprobValues(appendLogprobValues(initial, first, codec), second, codec)
					actual := MergeLogprobDeltas(initial, []Value{first, second}, codec)
					if actual != expected {
						t.Fatalf("%s/%d/%d/%d: got %s, want %s", codec, initialIndex, firstIndex, secondIndex, actual.JSONString(), expected.JSONString())
					}
				}
			}
			if actual := MergeLogprobDeltas(initial, nil, codec); actual != initial {
				t.Fatal("empty delta batch changed the original spelling")
			}
		}
	}
}

func TestCollectorDeferredLogprobsMatchImmediateObservation(t *testing.T) {
	for _, codec := range []string{"openai-chat", "responses"} {
		for _, bufferBytes := range []int{128, 512, 4096} {
			for _, stateItems := range []int{2, 4096} {
				t.Run(fmt.Sprintf("%s/%d/%d", codec, bufferBytes, stateItems), func(t *testing.T) {
					limits := DefaultLimits()
					limits.BufferBytes, limits.StateItems = bufferBytes, stateItems
					actual, err := NewResponseCollector(replayTarget(), limits)
					if err != nil {
						t.Fatal(err)
					}
					reference, _ := NewResponseCollector(replayTarget(), limits)
					metadata := func(value string, ordinal int) ResponseMetadata {
						location := "content"
						if codec == "openai-chat" {
							location = "choice"
							if value != "null" {
								value = `{"content":` + value + `}`
							}
						}
						return ResponseMetadata{Codec: codec, Location: location, Name: "logprobs", Path: fmt.Sprintf("/probabilities/%d", ordinal), Value: mustLogprobValue(t, value)}
					}
					events := []Event{messageEvent(ItemStarted, "t", "", &Node{Kind: TextNode, Metadata: []ResponseMetadata{metadata("null", 0)}})}
					for index := range 12 {
						event := messageEvent(ItemDelta, "t", "", nil)
						event.Delta = StringValue("x")
						event.Metadata = []ResponseMetadata{metadata(`[ {"token":"<>&","logprob":-1e-1,"bytes":[60,62,38],"top_logprobs":[]} ]`, index+1)}
						events = append(events, event)
					}
					events = append(events, messageEvent(ItemFinished, "t", "", &Node{Kind: TextNode, Payload: StringValue("xxxxxxxxxxxx")}), Event{SchemaVersion: 1, Type: ResponseFinished})
					before, _ := EncodeValue(events)
					hasDeferred := false
					for index, event := range events {
						kind, text, err := actual.Consume(event)
						hasDeferred = hasDeferred || actual.logprobItems > 0
						wantKind, wantText, wantErr := reference.Consume(event)
						// Materializing each accepted update reproduces the old
						// eager boundary; the pure merger is checked against the
						// unchanged pairwise implementation above.
						reference.materializeLogprobs()
						if kind != wantKind || text != wantText || fmt.Sprint(err) != fmt.Sprint(wantErr) {
							t.Fatalf("event %d: (%s,%q,%v) != (%s,%q,%v)", index, kind, text, err, wantKind, wantText, wantErr)
						}
						if index%5 == 0 || err != nil || event.Type == ItemFinished {
							assertCollectedLogprobSnapshot(t, actual, reference)
						}
						if err != nil {
							return
						}
						if event.Type == ItemFinished {
							node, ordinal, ok := actual.CompletedNode(event)
							wantNode, wantOrdinal, wantOK := reference.CompletedNode(event)
							wire, _ := EncodeValue(node)
							wantWire, _ := EncodeValue(wantNode)
							if wire != wantWire || ordinal != wantOrdinal || ok != wantOK {
								t.Fatal("completed node differs")
							}
						}
					}
					output, err := actual.Finish()
					expected, wantErr := reference.Finish()
					wire, _ := EncodeValue(output)
					wantWire, _ := EncodeValue(expected)
					if wire != wantWire || fmt.Sprint(err) != fmt.Sprint(wantErr) {
						t.Fatal("terminal snapshot differs", err, wantErr)
					}
					after, _ := EncodeValue(events)
					if before != after {
						t.Fatal("collector changed caller-owned events")
					}
					if !hasDeferred {
						t.Fatal("successful fixture never exercised deferred probabilities")
					}
				})
			}
		}
	}
}

func TestCollectorLogprobSnapshotsAndItemIsolation(t *testing.T) {
	for _, ending := range []string{"complete", "incomplete", "cancel"} {
		t.Run(ending, func(t *testing.T) {
			actual, _ := NewResponseCollector(replayTarget(), DefaultLimits())
			reference, _ := NewResponseCollector(replayTarget(), DefaultLimits())
			metadata := ResponseMetadata{Codec: "responses", Location: "content", Name: "logprobs", Path: "/original", Value: mustLogprobValue(t, `[]`)}
			consume := func(event Event) {
				t.Helper()
				kind, delta, err := actual.Consume(event)
				wantKind, wantDelta, wantErr := reference.Consume(event)
				reference.materializeLogprobs()
				if kind != wantKind || delta != wantDelta || fmt.Sprint(err) != fmt.Sprint(wantErr) {
					t.Fatal("event result changed", err, wantErr)
				}
			}
			for _, key := range []string{"left", "right"} {
				consume(messageEvent(ItemStarted, key, "", &Node{Kind: TextNode, Metadata: []ResponseMetadata{metadata}}))
			}
			for index := range 3 {
				for _, key := range []string{"left", "right"} {
					event := messageEvent(ItemDelta, key, "", nil)
					event.Delta = StringValue("x")
					next := metadata
					next.Path = fmt.Sprintf("/%s/%d", key, index)
					next.Value = mustLogprobValue(t, `[{"token":"x","logprob":-0.25,"bytes":[120]}]`)
					event.Metadata = []ResponseMetadata{next}
					consume(event)
				}
			}
			replacement := metadata
			replacement.Path = "/replacement"
			replacement.Value = mustLogprobValue(t, `[{"token":"snapshot","logprob":-1,"bytes":null}]`)
			consume(messageEvent(ItemSnapshot, "left", "", &Node{Kind: TextNode, Payload: StringValue("xxx"), Metadata: []ResponseMetadata{replacement}}))
			assertCollectedLogprobSnapshot(t, actual, reference)
			published, err := actual.Partial()
			if err != nil {
				t.Fatal(err)
			}
			published.Content[0].Metadata[0].Value = StringValue("caller mutation")
			assertCollectedLogprobSnapshot(t, actual, reference)
			switch ending {
			case "complete":
				for _, key := range []string{"left", "right"} {
					consume(messageEvent(ItemFinished, key, "", nil))
				}
				snapshot, _ := reference.Partial()
				consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: snapshot.Content}})
			case "cancel":
				consume(Event{SchemaVersion: 1, Type: OperationCancelled})
			}
			output, err := actual.Finish()
			expected, wantErr := reference.Finish()
			wire, _ := EncodeValue(output)
			wantWire, _ := EncodeValue(expected)
			if wire != wantWire || fmt.Sprint(err) != fmt.Sprint(wantErr) {
				t.Fatal("stream ending differs", err, wantErr)
			}
			if ending == "incomplete" && err == nil {
				t.Fatal("incomplete probability stream was accepted")
			}
			assertCollectedLogprobSnapshot(t, actual, reference)
		})
	}
}

func mustLogprobValue(t *testing.T, raw string) Value {
	t.Helper()
	value, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertCollectedLogprobSnapshot(t *testing.T, actual, reference *ResponseCollector) {
	t.Helper()
	left, err := actual.Partial()
	right, wantErr := reference.Partial()
	encoded, _ := EncodeValue(left)
	expected, _ := EncodeValue(right)
	if encoded != expected || fmt.Sprint(err) != fmt.Sprint(wantErr) || actual.bytes != reference.bytes {
		t.Fatal("partial content, error or original budget differs", err, wantErr)
	}
}
