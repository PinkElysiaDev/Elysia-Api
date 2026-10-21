package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestCollectorObservationBoundaries(t *testing.T) {
	collector, _ := NewResponseCollector(replayTarget(), DefaultLimits())
	consume := func(event Event) {
		t.Helper()
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	consume(messageEvent(ItemStarted, "text", "", &Node{Kind: TextNode}))
	text := ""
	for _, delta := range []string{"hello", "<>&\n", "世界", "\u2028"} {
		event := messageEvent(ItemDelta, "text", "", nil)
		event.Delta = StringValue(delta)
		consume(event)
		text += delta
		partial, err := collector.Partial()
		if err != nil || partial.Content[0].Payload != StringValue(text) {
			t.Fatalf("partial: %+v %v", partial, err)
		}
		// Observation must return independent snapshots when more deltas arrive.
		defer func(want string, snapshot *Response) {
			if snapshot.Content[0].Payload != StringValue(want) {
				t.Error("partial changed after publication")
			}
		}(text, partial)
	}
	end := messageEvent(ItemFinished, "text", "", &Node{Kind: TextNode, Payload: StringValue(text)})
	consume(end)
	node, ordinal, ok := collector.CompletedNode(end)
	if !ok || ordinal != 0 || node.Payload != StringValue(text) {
		t.Fatal("completed node lost text")
	}
	consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue(text)}}}})
	response, err := collector.Finish()
	if err != nil || response.Content[0].Payload != StringValue(text) {
		t.Fatal("terminal lost text", err)
	}
}

func TestCollectorDeferredTerminalMismatch(t *testing.T) {
	c, _ := NewResponseCollector(replayTarget(), DefaultLimits())
	for _, e := range []Event{messageEvent(ItemStarted, "t", "", &Node{Kind: TextNode}), {SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("t"), Delta: StringValue("real")}, messageEvent(ItemFinished, "t", "", nil)} {
		if _, _, err := c.Consume(e); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := c.Consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue("wrong")}}}})
	if err == nil || !strings.Contains(err.Error(), "terminal content") {
		t.Fatal("terminal comparison missed deferred text", err)
	}
}

// This fixture also runs unchanged against the pre-optimization source through
// a Go test overlay. Full collection (not just the append helper) is measured.
func BenchmarkAlgorithmCollector(b *testing.B) {
	for _, frames := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(frames), func(b *testing.B) {
			start := messageEvent(ItemStarted, "t", "", &Node{Kind: TextNode})
			delta := Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("t"), Delta: StringValue(strings.Repeat("a", 256))}
			end := messageEvent(ItemFinished, "t", "", nil)
			b.ReportAllocs()
			b.SetBytes(int64(frames * 256))
			for b.Loop() {
				c, _ := NewResponseCollector(replayTarget(), DefaultLimits())
				if _, _, err := c.Consume(start); err != nil {
					b.Fatal(err)
				}
				for range frames {
					if _, _, err := c.Consume(delta); err != nil {
						b.Fatal(err)
					}
				}
				for _, e := range []Event{end, {SchemaVersion: 1, Type: ResponseFinished}} {
					if _, _, err := c.Consume(e); err != nil {
						b.Fatal(err)
					}
				}
				r, err := c.Finish()
				if err != nil || r.Content[0].Payload.Len() != frames*256+2 {
					b.Fatal("invalid result", err)
				}
			}
		})
	}
}

func BenchmarkAlgorithmMessageCompletion(b *testing.B) {
	for _, count := range []int{128, 512, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			events := make([]Event, 0, count*4)
			for i := range count {
				parent, child := fmt.Sprint("m", i), fmt.Sprint("t", i)
				events = append(events, messageEvent(ItemStarted, parent, "", &Node{Kind: MessageNode, Role: StringValue("assistant")}), messageEvent(ItemStarted, child, parent, &Node{Kind: TextNode}), messageEvent(ItemFinished, child, parent, nil), messageEvent(ItemFinished, parent, "", nil))
			}
			limits := DefaultLimits()
			limits.StateItems = count * 4
			b.ReportAllocs()
			for b.Loop() {
				c, _ := NewResponseCollector(replayTarget(), limits)
				for _, e := range events {
					if _, _, err := c.Consume(e); err != nil {
						b.Fatal(err)
					}
					c.CompletedNode(e)
				}
			}
		})
	}
}
