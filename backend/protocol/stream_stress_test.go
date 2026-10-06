package protocol

import (
	"strings"
	"testing"
)

func TestLongReplayRetainsBoundedStateAndLateUsage(t *testing.T) {
	replay, err := NewEventReplay(replayTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}}); err != nil {
		t.Fatal(err)
	}
	chunk := StringValue(strings.Repeat("x", 4096))
	for range 4096 {
		if _, err := replay.Consume(Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("text"), Delta: chunk}); err != nil {
			t.Fatal(err)
		}
	}
	if replay.state.buffered != 0 || len(replay.items) != 1 || len(replay.state.texts) != 1 {
		t.Fatal("forwarded text grew retained state")
	}
	for _, event := range []Event{
		{SchemaVersion: 1, Type: ItemFinished, ItemID: StringValue("text")},
		{SchemaVersion: 1, Type: ResponseFinished},
		{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{Input: &Counter{Count: 100, Origin: ObservedCount}, CacheRead: &Counter{Count: 80, Origin: ObservedCount}}},
	} {
		if _, err := replay.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	if replay.Usage().CacheRead.Count != 80 {
		t.Fatal("long stream lost usage tail")
	}
}
