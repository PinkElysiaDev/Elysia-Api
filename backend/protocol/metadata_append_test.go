package protocol

import (
	"testing"
)

func TestCollectorAppendsDeclaredMetadataArrays(t *testing.T) {
	collector, err := NewResponseCollector(reasoningCollectionTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	start := reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode})
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	for index := range 8 {
		value, _ := EncodeValue([]int{index})
		event := reasoningCollectionEvent(ItemDelta, "text", "", "", nil)
		event.Delta = StringValue("")
		event.Metadata = []ResponseMetadata{
			{Codec: "custom", Location: "item", Name: "references", Value: value, Update: "append"},
			{Codec: "custom", Location: "item", Name: "observations", Value: value, Update: "append"},
		}
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
		if event.Metadata[0].Update != "append" {
			t.Fatal("caller metadata changed")
		}
	}
	if collector.metadataItems != 14 {
		t.Fatalf("incremental arrays were not deferred: %d", collector.metadataItems)
	}
	finish := reasoningCollectionEvent(ItemFinished, "text", "", "", nil)
	if _, _, err := collector.Consume(finish); err != nil {
		t.Fatal(err)
	}
	node, _, ok := collector.CompletedNode(finish)
	if !ok || len(node.Metadata) != 2 {
		t.Fatalf("completed metadata: %+v", node)
	}
	for _, metadata := range node.Metadata {
		if metadata.Update != "" || metadata.Value.JSONString() != "[0,1,2,3,4,5,6,7]" {
			t.Fatalf("collected array changed: %+v", metadata)
		}
	}
	if collector.metadataItems != 0 || collector.metadataBytes != 0 {
		t.Fatal("completed metadata retains delta buffers")
	}
}

func TestMetadataAppendRejectsInvalidUse(t *testing.T) {
	target := reasoningCollectionTarget()
	valid := ResponseMetadata{Codec: "custom", Location: "item", Name: "values", Value: fixtureValue(t, []int{1}), Update: "append"}
	for _, event := range []Event{
		{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Metadata: []ResponseMetadata{valid}},
		{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue(""), Metadata: []ResponseMetadata{{Update: "replace", Value: valid.Value}}},
		{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue(""), Metadata: []ResponseMetadata{{Update: "append", Value: StringValue("not an array")}}},
		{SchemaVersion: ProtocolCoreVersion, Type: ItemStarted, ItemID: StringValue("i"), Item: &Node{Kind: TextNode, Metadata: []ResponseMetadata{valid}}},
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, Response: &Response{SchemaVersion: ProtocolCoreVersion, Metadata: []ResponseMetadata{valid}}},
	} {
		if err := IssuesError(CheckEvent(event, target, DefaultLimits())); err == nil {
			t.Fatalf("accepted invalid metadata update: %+v", event)
		}
	}
	collector, _ := NewResponseCollector(target, DefaultLimits())
	initial := valid
	initial.Update, initial.Value = "", StringValue("scalar")
	if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "i", "", "", &Node{Kind: TextNode, Metadata: []ResponseMetadata{initial}})); err != nil {
		t.Fatal(err)
	}
	event := reasoningCollectionEvent(ItemDelta, "i", "", "", nil)
	event.Delta, event.Metadata = StringValue(""), []ResponseMetadata{valid}
	if _, _, err := collector.Consume(event); err == nil {
		t.Fatal("appended array to scalar snapshot")
	}
}

func TestMetadataAppendBudgetSurvivesFragmentCompaction(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes, limits.StateItems = 512, 2
	collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "i", "", "", &Node{Kind: TextNode})); err != nil {
		t.Fatal(err)
	}
	delta := reasoningCollectionEvent(ItemDelta, "i", "", "", nil)
	delta.Delta = StringValue("")
	delta.Metadata = []ResponseMetadata{{Codec: "custom", Location: "item", Name: "values", Update: "append", Value: fixtureValue(t, []string{"012345678901234567890123456789"})}}
	hasCompacted := false
	for range 32 {
		_, _, err := collector.Consume(delta)
		if err != nil {
			if !hasCompacted || !hasIssueCode(err, LimitExceeded) {
				t.Fatal("unexpected collection boundary", hasCompacted, err)
			}
			return
		}
		// A one-fragment window can compact and append in the same call.
		// Observe the growing snapshot instead of a drop in the queue length.
		key, _ := collector.replay.itemKey(delta)
		var values []Value
		if err := collector.item(key).Metadata[0].Value.Decode(&values); err != nil {
			t.Fatal(err)
		}
		hasCompacted = hasCompacted || len(values) > 1
	}
	t.Fatal("array append bypassed the collection budget after fragment compaction")
}

func TestMetadataEmptyAppendDoesNotAccumulateRetiredBrackets(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes, limits.StateItems = 512, 2
	collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "i", "", "", &Node{Kind: TextNode})); err != nil {
		t.Fatal(err)
	}
	delta := reasoningCollectionEvent(ItemDelta, "i", "", "", nil)
	delta.Delta = StringValue("")
	delta.Metadata = []ResponseMetadata{{Codec: "custom", Location: "item", Name: "values", Update: "append", Value: mustLogprobValue(t, "[  ]")}}
	if _, _, err := collector.Consume(delta); err != nil {
		t.Fatal(err)
	}
	before := collector.bytes
	for range 32 {
		if _, _, err := collector.Consume(delta); err != nil {
			t.Fatal(err)
		}
	}
	if collector.bytes != before {
		t.Fatal("empty append counted discarded array syntax", before, collector.bytes)
	}
}
