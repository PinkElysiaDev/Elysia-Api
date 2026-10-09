package protocol

import (
	"strings"
	"testing"
)

func messageEvent(kind EventType, id, parent string, node *Node) Event {
	e := Event{SchemaVersion: SemanticSchemaVersion, Type: kind, ItemID: StringValue(id), Item: node}
	if parent != "" {
		e.ParentID = StringValue(parent)
	}
	return e
}

func TestEventReplayMessageAssociations(t *testing.T) {
	start := messageEvent(ItemStarted, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant")})
	child := messageEvent(ItemStarted, "p", "m", &Node{Kind: TextNode, Payload: StringValue("hi")})
	for name, sequence := range map[string][]Event{
		"orphan":                  {child},
		"changed message role":    {start, messageEvent(ItemFinished, "m", "", &Node{Kind: MessageNode, Role: StringValue("user")})},
		"changed message ID":      {messageEvent(ItemStarted, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire1")}), messageEvent(ItemFinished, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire2")})},
		"parent finished early":   {start, child, messageEvent(ItemFinished, "m", "", nil)},
		"parent absent on update": {start, child, messageEvent(ItemFinished, "p", "", nil)},
		"changed parent":          {start, child, messageEvent(ItemStarted, "other", "", start.Item), messageEvent(ItemFinished, "p", "other", nil)},
		"nested message":          {start, messageEvent(ItemStarted, "nested", "m", start.Item)},
		"parent is text":          {messageEvent(ItemStarted, "m", "", child.Item), child},
		"closed parent":           {start, messageEvent(ItemFinished, "m", "", nil), child},
		"unterminated parent":     {start, {SchemaVersion: 1, Type: ResponseFinished}},
		"embedded children":       {messageEvent(ItemStarted, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{*child.Item}})},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := NewEventReplay(replayTarget(), DefaultLimits())
			for i, e := range sequence {
				_, err := r.Consume(e)
				if i == len(sequence)-1 {
					if err == nil {
						t.Fatal("invalid association accepted")
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMessageCollectorBoundariesAndContinuationOrdinals(t *testing.T) {
	c, _ := NewResponseCollector(replayTarget(), DefaultLimits())
	message := &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire_m"), Status: StringValue("in_progress")}
	text := &Node{Kind: TextNode, Payload: StringValue("a")}
	sequence := []Event{
		messageEvent(ItemStarted, "empty", "", &Node{Kind: MessageNode, Role: StringValue("assistant")}),
		messageEvent(ItemFinished, "empty", "", nil),
		messageEvent(ItemStarted, "m", "", message),
		messageEvent(ItemStarted, "a", "m", text), messageEvent(ItemFinished, "a", "m", text),
		messageEvent(ItemStarted, "b", "m", text), messageEvent(ItemFinished, "b", "m", text),
		messageEvent(ItemFinished, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire_m"), Status: StringValue("completed")}),
		messageEvent(ItemStarted, "r", "", &Node{Kind: ReasoningNode, Payload: StringValue("think")}), messageEvent(ItemFinished, "r", "", nil),
		{SchemaVersion: 1, Type: ResponseFinished},
	}
	var ordinals []int
	for _, e := range sequence {
		if _, _, err := c.Consume(e); err != nil {
			t.Fatal(err)
		}
		if _, ordinal, ok := c.CompletedNode(e); ok {
			ordinals = append(ordinals, ordinal)
		}
	}
	if len(ordinals) != 3 || ordinals[0] != 0 || ordinals[1] != 1 || ordinals[2] != 2 {
		t.Fatal(ordinals)
	}
	r, err := c.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 3 || len(r.Content[0].Children) != 0 || len(r.Content[1].Children) != 2 || r.Content[1].ID != StringValue("wire_m") {
		t.Fatalf("containers changed: %+v", r.Content)
	}
	// Explicit containers cannot be replaced by equivalent concatenated leaves.
	c, _ = NewResponseCollector(replayTarget(), DefaultLimits())
	for _, e := range sequence[:len(sequence)-1] {
		if _, _, err := c.Consume(e); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := c.Consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: []Node{*text, *text, {Kind: ReasoningNode, Payload: StringValue("think")}}}}); err == nil || !strings.Contains(err.Error(), "terminal content") {
		t.Fatal(err)
	}
}

func TestContinuationOrdinalWaitsForEarlierOpenMessage(t *testing.T) {
	c, _ := NewResponseCollector(replayTarget(), DefaultLimits())
	for _, e := range []Event{messageEvent(ItemStarted, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant")}), messageEvent(ItemStarted, "r", "", &Node{Kind: ReasoningNode, Payload: StringValue("x")})} {
		if _, _, err := c.Consume(e); err != nil {
			t.Fatal(err)
		}
	}
	e := messageEvent(ItemFinished, "r", "", nil)
	if _, _, err := c.Consume(e); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.CompletedNode(e); ok {
		t.Fatal("persisted an unstable leaf ordinal")
	}
}
