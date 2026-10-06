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

func TestResponseCollectorPreservesUnmappedStreamExtensions(t *testing.T) {
	// 与收集目标同族的溯源：回放层要求 Unmapped 原生内容可归属（CanPreserveNative）。
	family := Identity{Family: "collector-test", WireVersion: "1"}
	target := replayTarget()
	target.Protocol = family
	collector, err := NewResponseCollector(target, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	extensions, _ := ParseValue([]byte(`{"/":{"system_fingerprint":"fp"},"/usage":{"provider_metrics":{"write_tokens":31}}}`))
	provenance := Native{Source: Provenance{Protocol: family, Direction: DecodeEvent}, Value: extensions}
	events := []Event{
		{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode, Payload: StringValue("hi")}, Unmapped: &provenance},
		{SchemaVersion: 1, Type: ResponseFinished},
	}
	for _, event := range events {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatalf("unmapped extensions must not fail collection: %v", err)
		}
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	preserved, exists := response.Attributes["wire:stream"]
	if !exists || !strings.Contains(string(preserved.Bytes()), "provider_metrics") {
		t.Fatalf("unmapped stream extensions not preserved: %+v", response.Attributes)
	}

	bounded := DefaultLimits()
	bounded.BufferBytes = 64
	collector, err = NewResponseCollector(target, bounded)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}, Unmapped: &provenance}); err == nil {
		t.Fatal("unbounded unmapped extensions")
	}
}
