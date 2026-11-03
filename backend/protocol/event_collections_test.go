package protocol

import (
	"strings"
	"testing"
)

func reasoningCollectionEvent(kind EventType, id, parent, collection string, node *Node) Event {
	event := messageEvent(kind, id, parent, node)
	event.SchemaVersion, event.Collection = ProtocolCoreVersion, collection
	return event
}

func reasoningCollectionTarget() Target {
	target := replayTarget()
	target.coreVersion = ProtocolCoreVersion
	return target
}

func TestReasoningCollectionAssociations(t *testing.T) {
	parent := reasoningCollectionEvent(ItemStarted, "r", "", "", &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning})
	child := reasoningCollectionEvent(ItemStarted, "s", "r", "summary", &Node{Kind: TextNode, Payload: StringValue("")})
	finish := reasoningCollectionEvent(ItemFinished, "s", "r", "summary", nil)
	for name, events := range map[string][]Event{
		"orphan":                      {child},
		"no collection":               {parent, reasoningCollectionEvent(ItemStarted, "s", "r", "", child.Item)},
		"unknown collection":          {parent, reasoningCollectionEvent(ItemStarted, "s", "r", "other", child.Item)},
		"no parent":                   {reasoningCollectionEvent(ItemStarted, "s", "", "summary", child.Item)},
		"message parent":              {reasoningCollectionEvent(ItemStarted, "r", "", "", &Node{Kind: MessageNode, Role: StringValue("assistant")}), child},
		"scalar reasoning":            {reasoningCollectionEvent(ItemStarted, "r", "", "", &Node{Kind: ReasoningNode, Payload: StringValue("text")}), child},
		"summary has visible content": {reasoningCollectionEvent(ItemStarted, "r", "", "", &Node{Kind: ReasoningNode, ReasoningForm: SummaryReasoning}), reasoningCollectionEvent(ItemStarted, "s", "r", "reasoning_content", child.Item)},
		"nontext child":               {parent, reasoningCollectionEvent(ItemStarted, "s", "r", "summary", &Node{Kind: RefusalNode, Payload: StringValue("no")})},
		"changed collection":          {parent, child, reasoningCollectionEvent(ItemFinished, "s", "r", "reasoning_content", nil)},
		"parent closes early":         {parent, child, reasoningCollectionEvent(ItemFinished, "r", "", "", nil)},
		"child after parent":          {parent, reasoningCollectionEvent(ItemFinished, "r", "", "", nil), child},
		"terminal before parent":      {parent, child, finish, {SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished}},
		"embedded then child":         {reasoningCollectionEvent(ItemStarted, "r", "", "", &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, Children: []Node{*child.Item}}), child},
		"child then embedded":         {parent, child, reasoningCollectionEvent(ItemSnapshot, "r", "", "", &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, Children: []Node{*child.Item}})},
	} {
		t.Run(name, func(t *testing.T) {
			replay, err := NewEventReplay(reasoningCollectionTarget(), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			for index, event := range events {
				_, err = replay.Consume(event)
				if index == len(events)-1 {
					if err == nil {
						t.Fatal("accepted invalid reasoning association")
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestReasoningCollectorSeparatesIncrementalCollections(t *testing.T) {
	collector, err := NewResponseCollector(reasoningCollectionTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Spare capacity proves appending children never mutates a caller's slice.
	children := make([]Node, 0, 3)
	visible := make([]Node, 0, 3)
	parent := &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, Children: children, ReasoningContent: visible}
	sequence := []Event{
		reasoningCollectionEvent(ItemStarted, "r", "", "", parent),
		reasoningCollectionEvent(ItemStarted, "z", "r", "summary", &Node{Kind: TextNode, Payload: StringValue("brief")}),
		reasoningCollectionEvent(ItemStarted, "v", "r", "reasoning_content", &Node{Kind: TextNode, Payload: StringValue("thought")}),
		reasoningCollectionEvent(ItemStarted, "a", "r", "summary", &Node{Kind: TextNode, Payload: StringValue("second")}),
		{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("z"), ParentID: StringValue("r"), Collection: "summary", Delta: StringValue(" summary")},
		reasoningCollectionEvent(ItemFinished, "v", "r", "reasoning_content", nil),
		reasoningCollectionEvent(ItemFinished, "a", "r", "summary", nil),
		reasoningCollectionEvent(ItemFinished, "z", "r", "summary", nil),
		reasoningCollectionEvent(ItemFinished, "r", "", "", &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, Status: StringValue("completed"), Children: []Node{}, ReasoningContent: []Node{}}),
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished},
	}
	var observed strings.Builder
	completed := 0
	for _, event := range sequence {
		kind, delta, err := collector.Consume(event)
		if err != nil {
			t.Fatal(err)
		}
		if event.Collection != "" && kind != ReasoningNode {
			t.Fatalf("reasoning exposed as %s", kind)
		}
		observed.WriteString(delta)
		if node, ordinal, ok, err := collector.CompletedNode(event); err != nil {
			t.Fatal(err)
		} else if ok {
			completed++
			if node.Kind != ReasoningNode || ordinal != 0 || len(node.Children) != 2 || len(node.ReasoningContent) != 1 {
				t.Fatalf("invalid continuation: %+v, %d", node, ordinal)
			}
		}
	}
	result, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	node := result.Content[0]
	if completed != 1 || len(result.Content) != 1 || node.Children[0].Payload != StringValue("brief summary") || node.Children[1].Payload != StringValue("second") || node.ReasoningContent[0].Payload != StringValue("thought") {
		t.Fatalf("collections changed: %+v", result)
	}
	if observed.String() != "briefthoughtsecond summary" {
		t.Fatal(observed.String())
	}
	if children[:cap(children)][0].Kind != "" || visible[:cap(visible)][0].Kind != "" {
		t.Fatal("collector changed caller-owned slices")
	}
	// Re-accounting a parent with a long child must not serialize the prefix.
	key, _ := collector.replay.itemKey(sequence[0])
	before := collector.metadata[key]
	node.Children[0].Payload = StringValue(strings.Repeat("large", 1024))
	if err := collector.accountItemMetadata(key, node); err != nil || collector.metadata[key] != before {
		t.Fatalf("child counted as parent metadata: %d -> %d, %v", before, collector.metadata[key], err)
	}
}
