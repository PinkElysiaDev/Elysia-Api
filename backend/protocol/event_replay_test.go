package protocol

import "testing"

func replayTarget() Target {
	capabilities := CapabilitySet{}
	for _, capability := range CapabilityCatalog() {
		capabilities[capability] = true
	}
	return Target{Direction: EncodeEvent, Capabilities: capabilities}
}

func toolStart() Event {
	return Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("i"), Item: &Node{Kind: ToolCallNode, Name: StringValue("lookup"), CallID: StringValue("c"), Input: &ToolInput{Kind: JSONInput}}}
}

func TestEventReplayToolInputAndLateUsage(t *testing.T) {
	replay, err := NewEventReplay(replayTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{
		toolStart(),
		{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue(`{"n":`)},
		{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue(`9007199254740993}`)},
		{SchemaVersion: 1, Type: ItemFinished, ItemID: StringValue("i")},
		{SchemaVersion: 1, Type: ResponseFinished},
		{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{Input: &Counter{Count: 0, Origin: ObservedCount}, CacheRead: &Counter{Count: 0, Origin: ObservedCount}}},
	}
	for _, event := range events {
		if _, err := replay.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	if replay.state.buffered != 0 {
		t.Fatal("completed JSON tool retained input buffer")
	}
	usage := replay.Usage()
	if usage == nil || usage.Input == nil || usage.Input.Count != 0 || usage.CacheRead == nil {
		t.Fatalf("lost explicit-zero tail: %+v", usage)
	}
	usage.Input.Count = 999
	if replay.Usage().Input.Count != 0 {
		t.Fatal("returned usage aliases internal state")
	}
}

func TestEventReplayRejectsMalformedToolAndCumulativeRewrite(t *testing.T) {
	for _, text := range []string{"", "{", "invalid"} {
		replay, _ := NewEventReplay(replayTarget(), DefaultLimits())
		if _, err := replay.Consume(toolStart()); err != nil {
			t.Fatal(err)
		}
		if text != "" {
			if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue(text)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ResponseFinished}); err == nil {
			t.Fatalf("invalid arguments accepted: %q", text)
		}
	}
	replay, _ := NewEventReplay(replayTarget(), DefaultLimits())
	start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("prefix")}}
	if _, err := replay.Consume(start); err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ItemSnapshot, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("rewritten")}}); err == nil {
		t.Fatal("cumulative prefix rewrite accepted")
	}
}

func TestEventReplaySequenceAndIdentity(t *testing.T) {
	replay, _ := NewEventReplay(replayTarget(), DefaultLimits())
	start := toolStart()
	start.Sequence = fixtureValue(t, 1)
	if accepted, err := replay.Consume(start); err != nil || !accepted {
		t.Fatalf("first event: %t %v", accepted, err)
	}
	if accepted, err := replay.Consume(start); err != nil || accepted {
		t.Fatalf("duplicate event: %t %v", accepted, err)
	}
	changed := start
	changed.ItemID = StringValue("different")
	if _, err := replay.Consume(changed); err == nil {
		t.Fatal("conflicting event sequence accepted")
	}
	replay, _ = NewEventReplay(replayTarget(), DefaultLimits())
	index := 0
	start = toolStart()
	start.ItemID, start.Index, start.Item.CallID = Value{}, &index, Value{}
	if _, err := replay.Consume(start); err != nil {
		t.Fatal(err)
	}
	finish := Event{SchemaVersion: 1, Type: ItemFinished, ItemID: StringValue("late"), Index: &index, CallID: StringValue("c"), Item: &Node{Kind: ToolCallNode, Name: StringValue("lookup"), CallID: StringValue("c"), Input: &ToolInput{Kind: JSONInput, Value: fixtureValue(t, map[string]any{"ok": true})}}}
	if _, err := replay.Consume(finish); err != nil {
		t.Fatal(err)
	}
	if len(replay.items) != 1 {
		t.Fatal("late identity split the tool item")
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ResponseFinished}); err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ResponseFinished}); err == nil {
		t.Fatal("unsequenced duplicate terminal accepted")
	}
}

func TestEventReplayCancellationAndUnexpectedEOF(t *testing.T) {
	replay, _ := NewEventReplay(replayTarget(), DefaultLimits())
	if _, err := replay.Consume(toolStart()); err != nil {
		t.Fatal(err)
	}
	if err := replay.Finish(); err == nil {
		t.Fatal("EOF pretended to be success")
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: OperationCancelled}); err != nil {
		t.Fatal(err)
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue("{}")}); err == nil {
		t.Fatal("content after cancellation accepted")
	}
}
