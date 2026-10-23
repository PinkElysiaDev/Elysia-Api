package builtin

import (
	"fmt"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestTargetDeferredLogprobsPreserveFramesAndBudget(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		for _, limit := range []int{256, 1024, 8192} {
			for _, stateItems := range []int{2, 4096} {
				for _, ending := range []string{"explicit", "implicit", "cancel", "wrong-codec"} {
					t.Run(fmt.Sprintf("%s/%d/%d/%s", codec, limit, stateItems, ending), func(t *testing.T) {
						limits := p.DefaultLimits()
						limits.BufferBytes, limits.StateItems = limit, stateItems
						newStream := func() *streamModule {
							instance, err := (module{name: codec}).NewStream(p.EncodeEvent, limits)
							if err != nil {
								t.Fatal(err)
							}
							return instance.(*streamModule)
						}
						actual, reference := newStream(), newStream()
						events := targetLogprobEvents(t, codec, ending)
						before, _ := p.EncodeValue(events)
						hasDeferred := false
						for index, event := range events {
							frames, err := actual.EncodeEventFrames(t.Context(), event, p.EvaluationContext{})
							hasDeferred = hasDeferred || actual.logprobItems > 0
							wantFrames, wantErr := reference.EncodeEventFrames(t.Context(), event, p.EvaluationContext{})
							reference.materializeLogprobs()
							if !reflect.DeepEqual(frames, wantFrames) || fmt.Sprint(err) != fmt.Sprint(wantErr) || actual.buffered != reference.buffered {
								t.Fatalf("event %d changed frames, error or budget: %v / %v", index, err, wantErr)
							}
							if err != nil {
								return
							}
							if event.Type == p.ItemDelta && len(event.Metadata) > 0 && len(frames) == 0 {
								t.Fatal("probability delta was delayed until item completion")
							}
						}
						frames, err := actual.Finish(t.Context(), p.EvaluationContext{})
						wantFrames, wantErr := reference.Finish(t.Context(), p.EvaluationContext{})
						if !reflect.DeepEqual(frames, wantFrames) || fmt.Sprint(err) != fmt.Sprint(wantErr) || actual.buffered != reference.buffered {
							t.Fatal("terminal output or budget changed", err, wantErr)
						}
						if !hasDeferred {
							t.Fatal("fixture never exercised deferred probabilities")
						}
						after, _ := p.EncodeValue(events)
						if before != after {
							t.Fatal("target changed caller-owned events")
						}
					})
				}
			}
		}
	}
}

func targetLogprobEvents(t *testing.T, codec, ending string) []p.Event {
	t.Helper()
	location, probabilities := "content", auditLogprobs
	if codec == Chat {
		location, probabilities = "choice", `{"content":`+auditLogprobs+`}`
	}
	metadata := p.ResponseMetadata{Codec: codec, Location: location, Name: "logprobs", Value: testValue(t, probabilities)}
	events := []p.Event{
		{SchemaVersion: 1, Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
		{SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue("text")}},
	}
	for index := range 9 {
		next := metadata
		next.Path = fmt.Sprintf("/probabilities/%d", index)
		if ending == "wrong-codec" && index == 5 {
			next.Codec = "unsupported"
		}
		text := "x"
		if index%3 == 0 {
			text = "" // Metadata-only events must still emit immediately.
		}
		events = append(events, p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue(text), Metadata: []p.ResponseMetadata{next}})
	}
	if ending == "cancel" {
		return append(events, p.Event{SchemaVersion: 1, Type: p.OperationCancelled})
	}
	if ending == "explicit" {
		events = append(events, p.Event{SchemaVersion: 1, Type: p.ItemFinished, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("xxxxxx")}})
	}
	events = append(events,
		p.Event{SchemaVersion: 1, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}},
		p.Event{SchemaVersion: 1, Type: p.UsageUpdated, Usage: &p.Usage{Input: &p.Counter{Count: 7, Origin: p.ObservedCount}, Output: &p.Counter{Count: 0, Origin: p.ObservedCount}}},
	)
	return events
}

func TestTargetLogprobSnapshotsKeepParallelItems(t *testing.T) {
	newStream := func() *streamModule {
		instance, err := (module{name: Responses}).NewStream(p.EncodeEvent, p.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		return instance.(*streamModule)
	}
	actual, reference := newStream(), newStream()
	consume := func(event p.Event) {
		t.Helper()
		frames, err := actual.EncodeEventFrames(t.Context(), event, p.EvaluationContext{})
		expected, wantErr := reference.EncodeEventFrames(t.Context(), event, p.EvaluationContext{})
		reference.materializeLogprobs()
		if !reflect.DeepEqual(frames, expected) || fmt.Sprint(err) != fmt.Sprint(wantErr) || actual.buffered != reference.buffered {
			t.Fatal("parallel item frames or budget differ", err, wantErr)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	consume(targetLogprobEvents(t, Responses, "implicit")[0])
	for _, key := range []string{"left", "right"} {
		consume(p.Event{SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue(key), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue(key)}})
	}
	for index := range 4 {
		for _, key := range []string{"left", "right"} {
			metadata := p.ResponseMetadata{Codec: Responses, Location: "content", Name: "logprobs", Path: "/" + key, Value: testValue(t, auditLogprobs)}
			event := p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: p.StringValue(key), Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{metadata}}
			if index == 3 && key == "left" {
				metadata.Value = testValue(t, `[]`)
				event.Type, event.Delta, event.Metadata = p.ItemSnapshot, p.Value{}, nil
				event.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("xxx"), Metadata: []p.ResponseMetadata{metadata}}
			}
			consume(event)
		}
	}
	consume(p.Event{SchemaVersion: 1, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}})
	frames, err := actual.Finish(t.Context(), p.EvaluationContext{})
	expected, wantErr := reference.Finish(t.Context(), p.EvaluationContext{})
	if !reflect.DeepEqual(frames, expected) || fmt.Sprint(err) != fmt.Sprint(wantErr) || actual.logprobItems != 0 || actual.logprobBytes != 0 {
		t.Fatal("parallel item terminal differs or retained pending fragments", err, wantErr)
	}
}
