package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestCollectorDeduplicatesBeforeRetainingExtensions(t *testing.T) {
	target := replayTarget()
	target.Protocol = Identity{Family: "collector", WireVersion: "1"}
	limits := DefaultLimits()
	limits.StateItems = 2
	collector, err := NewResponseCollector(target, limits)
	if err != nil {
		t.Fatal(err)
	}
	event := messageEvent(ItemStarted, "text", "", &Node{Kind: TextNode})
	event.Sequence = coreInteger(1)
	event.Unmapped = &Native{Source: Provenance{Protocol: target.Protocol, Direction: DecodeEvent}, Value: parseTestValue(t, `{"extra":1}`)}
	for range 4 {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if len(collector.unmapped) != 1 || collector.retainedItems() != 2 {
		t.Fatal("duplicate extension consumed another state item")
	}
	event = messageEvent(ItemDelta, "text", "", nil)
	event.Delta = StringValue("next")
	event.Unmapped = &Native{Source: Provenance{Protocol: target.Protocol, Direction: DecodeEvent}, Value: parseTestValue(t, `{"extra":2}`)}
	if _, _, err := collector.Consume(event); !hasIssueCode(err, LimitExceeded) || len(collector.unmapped) != 1 {
		t.Fatal("extensions and content did not share their item budget", err)
	}
}

func TestCollectorRootMetadataSharesContentBudget(t *testing.T) {
	for _, bound := range []string{"bytes", "items"} {
		t.Run(bound, func(t *testing.T) {
			limits := DefaultLimits()
			if bound == "bytes" {
				limits.BufferBytes = 512
			} else {
				limits.StateItems = 3
			}
			collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := collector.Consume(reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode, Payload: StringValue(strings.Repeat("x", 180))})); err != nil {
				t.Fatal(err)
			}
			for index := range 8 {
				event := Event{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Metadata: []ResponseMetadata{{Codec: "custom", Location: "response", Name: fmt.Sprint(index), Value: StringValue(strings.Repeat("m", 20))}}}
				_, _, err := collector.Consume(event)
				if err != nil {
					if !hasIssueCode(err, LimitExceeded) || len(collector.response.Metadata) != index {
						t.Fatal("failed root update was retained", index, err)
					}
					if _, err := collector.Finish(); err == nil {
						t.Fatal("failed collection produced a successful result")
					}
					return
				}
				if _, _, err := collector.Consume(event); err != nil || len(collector.response.Metadata) != index+1 {
					t.Fatal("repeated metadata snapshot used another slot", err)
				}
			}
			t.Fatal("root metadata escaped the content budget")
		})
	}
}

func TestCollectorInitialSnapshotSharesLaterContentBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 512
	collector, err := NewResponseCollector(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Response{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("a", 220))}}}
	if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: ResponseStarted, Response: snapshot}); err != nil {
		t.Fatal(err)
	}
	event := messageEvent(ItemStarted, "next", "", &Node{Kind: TextNode, Payload: StringValue(strings.Repeat("b", 220))})
	if _, _, err := collector.Consume(event); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("initial response snapshot bypassed incremental collection budget", err)
	}
}

func TestCollectorTerminalSnapshotSharesMetadataBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 2
	collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	root := Event{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Metadata: []ResponseMetadata{{Codec: "custom", Location: "response", Name: "extra", Value: StringValue("kept")}}}
	if _, _, err := collector.Consume(root); err != nil {
		t.Fatal(err)
	}
	terminal := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, Response: &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: TextNode}, {Kind: TextNode}}}}
	if _, _, err := collector.Consume(terminal); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("terminal response received a separate item budget", err)
	}
	if len(collector.response.Content) != 0 {
		t.Fatal("failed terminal snapshot replaced the collected response")
	}
	if _, err := collector.Finish(); err == nil {
		t.Fatal("replay terminal hid a collection failure")
	}
}

func TestCollectorLegacyMetadataBudgetSurvivesCompaction(t *testing.T) {
	for _, codec := range []string{"openai-chat", "responses"} {
		t.Run(codec, func(t *testing.T) {
			limits := DefaultLimits()
			limits.BufferBytes, limits.StateItems = 512, 2
			collector, err := NewResponseCollector(replayTarget(), limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := collector.Consume(messageEvent(ItemStarted, "text", "", &Node{Kind: TextNode, Payload: StringValue(strings.Repeat("x", 128))})); err != nil {
				t.Fatal(err)
			}
			metadata := ResponseMetadata{Codec: codec, Location: "content", Name: "logprobs", Value: parseTestValue(t, `[{"token":"x","logprob":-0.1,"bytes":[120]}]`)}
			if codec == "openai-chat" {
				metadata.Location = "choice"
				metadata.Value = fixtureValue(t, Object{"content": metadata.Value})
			}
			delta := messageEvent(ItemDelta, "text", "", nil)
			delta.Delta, delta.Metadata = StringValue(""), []ResponseMetadata{metadata}
			for range 32 {
				_, _, err := collector.Consume(delta)
				if err != nil {
					if !hasIssueCode(err, LimitExceeded) {
						t.Fatal(err)
					}
					return
				}
				if collector.bytes > limits.BufferBytes || collector.retainedItems()+collector.metadataItems > limits.StateItems {
					t.Fatal("accepted metadata exceeded the combined retention budget")
				}
			}
			t.Fatal("materialized probabilities escaped their retained byte budget")
		})
	}
}

func TestCollectorTerminalSnapshotDoesNotDoubleCountStreamedNodes(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 1
	collector, err := NewResponseCollector(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	node := Node{Kind: TextNode, Payload: StringValue("done")}
	for _, event := range []Event{
		messageEvent(ItemStarted, "text", "", &node),
		messageEvent(ItemFinished, "text", "", nil),
		{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: []Node{node}}},
	} {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := collector.Finish(); err != nil || len(result.Content) != 1 || collector.retainedItems() != 1 || len(collector.texts) != 0 {
		t.Fatal("terminal retained a second copy of streamed content", err)
	}
}

func TestCollectorCancellationReleasesAccumulationBuffers(t *testing.T) {
	collector, err := NewResponseCollector(replayTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	start := toolStart()
	for _, event := range []Event{
		start,
		{SchemaVersion: 1, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue(`{"partial":`)},
		messageEvent(ItemStarted, "text", "", &Node{Kind: TextNode, Payload: StringValue("visible")}),
	} {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: OperationCancelled}); err == nil {
		t.Fatal("cancelled collection succeeded")
	}
	if len(collector.texts) != 0 || collector.metadataItems != 0 || collector.replay.state.buffered != 0 {
		t.Fatal("cancellation retained accumulation buffers")
	}
	partial, err := collector.Partial()
	if err != nil || !partial.Status.IsString("incomplete") || !partial.Content[1].Payload.IsString("visible") {
		t.Fatal("cancellation lost bounded diagnostic content", partial, err)
	}
}

func TestCollectorSharesByteBudgetWithReplay(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 512
	collector, err := NewResponseCollector(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	start := toolStart()
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	// Each owner fits alone. Together the response copy and replay's unfinished
	// JSON input exceed one response's capacity, so the delta cannot be published.
	delta := Event{SchemaVersion: 1, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue(strings.Repeat(" ", 220))}
	if collector.bytes+220 > limits.BufferBytes || collector.replay.retainedBytes()+220 > limits.BufferBytes {
		t.Fatal("fixture exceeds a single holder's capacity")
	}
	if _, text, err := collector.Consume(delta); !hasIssueCode(err, LimitExceeded) || text != "" {
		t.Fatal("collector and replay acquired separate byte budgets", text, err)
	}
	if len(collector.texts) != 0 || collector.replay.state.buffered != 0 {
		t.Fatal("combined budget failure retained accumulation buffers")
	}
	if _, err := collector.Finish(); err == nil {
		t.Fatal("combined budget failure produced a successful response")
	}
}
