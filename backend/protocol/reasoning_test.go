package protocol

import "testing"

func TestReasoningCanonicalizationRetainsPartsAndState(t *testing.T) {
	part := Node{Kind: TextNode, Payload: StringValue("full")}
	node := Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{part}}
	plain := CanonicalReasoning(node)
	if plain.ReasoningForm != "" || plain.Payload != part.Payload || plain.ReasoningContent != nil {
		t.Fatal(plain)
	}
	if ContinuationNodeDigest(node) != ContinuationNodeDigest(plain) {
		t.Fatal("equivalent single part changed continuation digest")
	}
	for _, extra := range []Node{
		{Kind: TextNode, Payload: StringValue("full"), Attributes: Object{"vendor": StringValue("state")}},
		{Kind: TextNode, Payload: StringValue("full"), Resources: []Resource{{Kind: "signature", ID: StringValue("sig")}}},
		{Kind: TextNode, Payload: StringValue("full"), ID: StringValue("part-id")},
	} {
		node.ReasoningContent = []Node{extra}
		if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
			t.Fatal("part fields discarded")
		}
	}
	node.ReasoningContent = []Node{part, part}
	if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
		t.Fatal("multiple parts merged")
	}
	node.ReasoningContent = []Node{part}
	node.Children = []Node{{Kind: TextNode, Payload: StringValue("brief")}}
	if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
		t.Fatal("summary discarded")
	}
	if ContinuationNodeDigest(node) == ContinuationNodeDigest(plain) {
		t.Fatal("mixed content authenticated as plain thinking")
	}
}

func TestStructuredReasoningReplayRejectsPartRewrites(t *testing.T) {
	for _, changed := range [][]Node{
		{{Kind: TextNode, Payload: StringValue("replacement")}}, {},
	} {
		replay, err := NewEventReplay(Target{Capabilities: CapabilitySet{ReasoningCapability: true, TextCapability: true}}, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("r"), Item: &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("original")}}}}
		if _, err = replay.Consume(start); err != nil {
			t.Fatal(err)
		}
		changedEvent := start
		changedEvent.Type = ItemSnapshot
		node := *start.Item
		node.ReasoningContent = changed
		changedEvent.Item = &node
		if _, err = replay.Consume(changedEvent); err == nil {
			t.Fatal("emitted part changed")
		}
	}
}
