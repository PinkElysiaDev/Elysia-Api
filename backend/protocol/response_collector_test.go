package protocol

import (
	"strings"
	"testing"
)

func TestResponseCollectorInterleavedToolsSnapshotsAndTail(t *testing.T) {
	collector, err := NewResponseCollector(replayTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	start := toolStart()
	start.Index, start.ItemID, start.Item.CallID = &index, Value{}, Value{}
	events := []Event{
		start,
		{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("hello")}},
		{SchemaVersion: 1, Type: ItemDelta, Index: &index, ItemID: StringValue("later"), CallID: StringValue("c"), Delta: StringValue(`{"n":9007199254740993}`)},
		{SchemaVersion: 1, Type: ItemSnapshot, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("hello world")}},
		{SchemaVersion: 1, Type: ResponseFinished},
		{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{CacheRead: &Counter{Count: 21, Origin: ObservedCount}, Output: &Counter{Count: 0, Origin: ObservedCount}}},
	}
	var streamed strings.Builder
	for _, event := range events {
		_, text, err := collector.Consume(event)
		if err != nil {
			t.Fatal(err)
		}
		streamed.WriteString(text)
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "hello world" || len(response.Content) != 2 || string(response.Content[0].Input.Value.Bytes()) != `{"n":9007199254740993}` || string(response.Content[0].CallID.Bytes()) != `"c"` {
		t.Fatalf("collected: %+v; streamed=%q", response, streamed.String())
	}
	if response.Usage.CacheRead.Count != 21 || response.Usage.Output.Count != 0 {
		t.Fatal("usage tail lost")
	}
}

func TestResponseCollectorDoesNotMaskFailuresOrGrowWithoutLimit(t *testing.T) {
	collector, _ := NewResponseCollector(replayTarget(), DefaultLimits())
	if _, err := collector.Finish(); err == nil {
		t.Fatal("accepted missing terminal")
	}
	if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: OperationFailed, Error: StringValue("failed")}); err == nil {
		t.Fatal("accepted failure")
	}
	limits := DefaultLimits()
	limits.BufferBytes = 256
	collector, err := NewResponseCollector(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("prefix")}}
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue(strings.Repeat("x", limits.BufferBytes))}); err == nil {
		t.Fatal("unbounded collection")
	}
}
