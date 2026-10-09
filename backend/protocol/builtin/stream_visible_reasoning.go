package builtin

import (
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeVisibleReasoningFrame(kind, key string, fields p.Object) ([]p.Event, error) {
	item := stream.items[key]
	if item == nil || item.node.Kind != p.ReasoningNode || item.node.ReasoningForm != "" {
		return nil, unsupported("/item_id", "reasoning text requires a visible reasoning item")
	}
	index, err := frameIndex(fields["content_index"])
	if err != nil || index != 0 {
		return nil, unsupported("/content_index", "visible reasoning part requires its ordered content index")
	}
	if item.isFinished || item.visiblePartDone {
		return nil, fmt.Errorf("visible reasoning received content after completion")
	}
	value := fields["text"]
	if kind == "response.content_part.added" || kind == "response.content_part.done" {
		part, err := fields["part"].ReadObject()
		if err != nil {
			return nil, err
		}
		if part["type"] != p.StringValue("reasoning_text") {
			return nil, unsupported("/part/type", "visible reasoning requires reasoning_text")
		}
		value = part["text"]
	}
	if kind == "response.content_part.added" {
		if item.visiblePartStarted {
			return nil, fmt.Errorf("visible reasoning part started twice")
		}
		item.visiblePartStarted = true
	} else if !item.visiblePartStarted {
		return nil, fmt.Errorf("visible reasoning update has no part start")
	}
	isDelta := kind == "response.reasoning_text.delta"
	if isDelta {
		value = fields["delta"]
	}
	text, err := stringValue(value)
	if err != nil {
		return nil, err
	}
	if item.visibleTextDone && (kind != "response.content_part.done" || text != item.buffer.String()) {
		return nil, fmt.Errorf("completed reasoning text changed")
	}
	delta := text
	if !isDelta {
		delta, err = item.text.Snapshot(text)
		if err != nil {
			return nil, err
		}
	} else {
		item.text.Append(delta)
	}
	if stream.buffered+len(delta) > stream.limits.BufferBytes {
		return nil, unsupported("/content", "reasoning content exceeds stream buffer limit")
	}
	item.buffer.WriteString(delta)
	stream.buffered += len(delta)
	if kind == "response.reasoning_text.done" {
		item.visibleTextDone = true
	}
	if kind == "response.content_part.done" {
		if !item.visibleTextDone {
			return nil, fmt.Errorf("reasoning part completed before its text")
		}
		item.visiblePartDone = true
	}
	if isDelta {
		event, err := stream.itemEvent(p.ItemDelta, key, nil, p.StringValue(delta))
		return []p.Event{event}, err
	}
	node := item.node
	node.Payload = p.StringValue(item.buffer.String())
	event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
	return []p.Event{event}, err
}
