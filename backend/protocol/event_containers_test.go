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
	tool := toolStart()
	tool.ParentID = start.ItemID
	for name, sequence := range map[string][]Event{
		"orphan":                  {child},
		"changed message role":    {start, messageEvent(ItemFinished, "m", "", &Node{Kind: MessageNode, Role: StringValue("user")})},
		"changed message ID":      {messageEvent(ItemStarted, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire1")}), messageEvent(ItemFinished, "m", "", &Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("wire2")})},
		"parent finished early":   {start, child, messageEvent(ItemFinished, "m", "", nil)},
		"parent before tool":      {start, tool, messageEvent(ItemFinished, "m", "", nil)},
		"parent absent on update": {start, child, messageEvent(ItemFinished, "p", "", nil)},
		"changed parent":          {start, child, messageEvent(ItemStarted, "other", "", start.Item), messageEvent(ItemFinished, "p", "other", nil)},
		"nested message":          {start, messageEvent(ItemStarted, "nested", "m", start.Item)},
		"structured reasoning":    {start, messageEvent(ItemStarted, "reasoning", "m", &Node{Kind: ReasoningNode, ReasoningForm: SummaryReasoning})},
		"media child":             {start, messageEvent(ItemStarted, "image", "m", &Node{Kind: ImageNode})},
		"nested snapshot":         {start, child, messageEvent(ItemSnapshot, "p", "m", &Node{Kind: TextNode, Children: []Node{*child.Item}})},
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

func TestMessageCollectorThinkingAndToolChildren(t *testing.T) {
	target := replayTarget()
	target.coreVersion = ProtocolCoreVersion
	target.Scope = Scope{Provider: "fixture", Account: "account"}
	collector, err := NewResponseCollector(target, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	message := Node{Kind: MessageNode, Role: StringValue("assistant"), ID: StringValue("message")}
	thinking := Node{Kind: ReasoningNode, Payload: StringValue("think"), Resources: []Resource{{Kind: "signature", ID: StringValue("signature"), Scope: target.Scope}}}
	redacted := Node{Kind: ReasoningNode, Resources: []Resource{{Kind: "encrypted_content", ID: StringValue("encrypted"), Scope: target.Scope}}}
	tool := Node{Kind: ToolCallNode, Name: StringValue("lookup"), CallID: StringValue("call"), Input: &ToolInput{Kind: JSONInput}}
	text := Node{Kind: TextNode, Payload: StringValue("answer")}
	toolPrefix := messageEvent(ItemDelta, "tool", "message", nil)
	toolPrefix.Delta = StringValue(`{"n":`)
	toolSuffix := toolPrefix
	toolSuffix.Delta = StringValue(`9007199254740993}`)
	thinkingDelta := messageEvent(ItemDelta, "thinking", "message", nil)
	thinkingDelta.Delta = thinking.Payload
	sequence := []Event{
		messageEvent(ItemStarted, "message", "", &message),
		messageEvent(ItemStarted, "thinking", "message", &Node{Kind: ReasoningNode}),
		thinkingDelta, messageEvent(ItemFinished, "thinking", "message", &thinking),
		messageEvent(ItemStarted, "redacted", "message", &redacted), messageEvent(ItemFinished, "redacted", "message", &redacted),
		messageEvent(ItemStarted, "tool", "message", &tool), toolPrefix, toolSuffix, messageEvent(ItemFinished, "tool", "message", nil),
		messageEvent(ItemStarted, "text", "message", &text), messageEvent(ItemFinished, "text", "message", &text),
		messageEvent(ItemFinished, "message", "", &message),
	}
	expectedKinds := []NodeKind{ReasoningNode, ReasoningNode, ToolCallNode, TextNode}
	completed := 0
	for _, event := range sequence {
		event.SchemaVersion = ProtocolCoreVersion
		kind, delta, err := collector.Consume(event)
		if err != nil {
			t.Fatal(event.Type, event.ItemID, err)
		}
		if delta != "" && kind != ReasoningNode && kind != TextNode {
			t.Fatal("tool input became assistant output", kind, delta)
		}
		if node, ordinal, ok, err := collector.CompletedNode(event); err != nil {
			t.Fatal(err)
		} else if ok {
			if ordinal != completed || node.Kind != expectedKinds[completed] {
				t.Fatal("completed child lost its kind or continuation position", node.Kind, ordinal, completed)
			}
			if node.Kind == ToolCallNode && (!node.CallID.IsString("call") || node.Input.Value.raw != `{"n":9007199254740993}`) {
				t.Fatal("completed tool lost its identity or exact arguments", node)
			}
			completed++
		}
	}
	finishedTool := tool
	finishedTool.Input = &ToolInput{Kind: JSONInput, Value: parseTestValue(t, `{"n":9007199254740993}`)}
	message.Children = []Node{thinking, redacted, finishedTool, text}
	terminal := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, Response: &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{message}}}
	if _, _, err := collector.Consume(terminal); err != nil {
		t.Fatal("closed child sequence disagreed with its JSON container", err)
	}
	response, err := collector.Finish()
	if err != nil || completed != len(expectedKinds) || len(response.Content) != 1 || len(response.Content[0].Children) != len(expectedKinds) || !response.Content[0].Children[1].Payload.IsZero() {
		t.Fatal("container collection lost content or invented redacted text", response, completed, err)
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
		if _, ordinal, ok, err := c.CompletedNode(e); err != nil {
			t.Fatal(err)
		} else if ok {
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
	if _, _, ok, err := c.CompletedNode(e); err != nil || ok {
		t.Fatal("persisted an unstable leaf ordinal", err)
	}
}
