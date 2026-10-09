package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeVisibleReasoningFrame(kind, key string, fields p.Object) ([]p.Event, error) {
	item := stream.items[key]
	if item == nil || item.node.ReasoningForm != p.StructuredReasoning || item.isFinished {
		return nil, unsupported("/item_id", "reasoning text requires an active structured reasoning item")
	}
	index, err := frameIndex(fields["content_index"])
	if err != nil {
		return nil, err
	}
	node := item.node
	node.ReasoningContent = append([]p.Node{}, node.ReasoningContent...)
	if kind == "response.content_part.added" {
		if index != len(node.ReasoningContent) || index >= stream.limits.StateItems {
			return nil, unsupported("/content_index", "reasoning part must append at the next bounded content index")
		}
		part, err := stream.module.decodeReasoningPart(fields["part"], "reasoning_text", "/part")
		if err != nil {
			return nil, err
		}
		node.ReasoningContent = append(node.ReasoningContent, part)
	} else {
		if index >= len(node.ReasoningContent) {
			return nil, unsupported("/content_index", "reasoning update has no part start")
		}
		if item.visiblePartDone[index] {
			return nil, unsupported("/content_index", "reasoning content arrived after part completion")
		}
		if item.visibleTextDone[index] && kind != "response.content_part.done" {
			return nil, unsupported("/content_index", "reasoning content arrived after text completion")
		}
		previous, err := stringValue(node.ReasoningContent[index].Payload)
		if err != nil {
			return nil, err
		}
		value := fields["text"]
		if kind == "response.reasoning_text.delta" {
			value = fields["delta"]
		}
		if kind == "response.content_part.done" {
			if !item.visibleTextDone[index] {
				return nil, unsupported("/content_index", "reasoning part completed before its text")
			}
			part, err := stream.module.decodeReasoningPart(fields["part"], "reasoning_text", "/part")
			if err != nil {
				return nil, err
			}
			value = part.Payload
			node.ReasoningContent[index].Attributes = part.Attributes
		}
		text, err := stringValue(value)
		if err != nil {
			return nil, reasoningInputError("/text", "reasoning event requires string text")
		}
		if kind == "response.reasoning_text.delta" {
			if stream.buffered+len(text) > stream.limits.BufferBytes {
				return nil, unsupported("/content", "reasoning content exceeds stream buffer limit")
			}
			text = previous + text
		} else if !strings.HasPrefix(text, previous) {
			return nil, unsupported("/text", "reasoning snapshot rewrote emitted text")
		}
		if item.visibleTextDone[index] && text != previous {
			return nil, unsupported("/text", "completed reasoning text changed")
		}
		if kind == "response.reasoning_text.done" {
			if item.visibleTextDone == nil {
				item.visibleTextDone = map[int]bool{}
			}
			item.visibleTextDone[index] = true
		}
		if kind == "response.content_part.done" {
			if item.visiblePartDone == nil {
				item.visiblePartDone = map[int]bool{}
			}
			item.visiblePartDone[index] = true
		}
		node.ReasoningContent[index].Payload = p.StringValue(text)
	}
	event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
	return []p.Event{event}, err
}

func (stream *streamModule) checkReasoningCompletion(key string, node p.Node) error {
	item := stream.items[key]
	if item == nil {
		return unsupported("/item", "reasoning completion has no item start")
	}
	for index, old := range item.node.ReasoningContent {
		if index >= len(node.ReasoningContent) {
			return unsupported("/item/content", "reasoning completion removed a part")
		}
		if !item.visiblePartDone[index] {
			return unsupported("/item/content", "reasoning item completed before its content part")
		}
		before, _ := stringValue(old.Payload)
		after, _ := stringValue(node.ReasoningContent[index].Payload)
		if before != after {
			return unsupported("/item/content", "completed reasoning content changed")
		}
	}
	for index, old := range item.node.Children {
		if index >= len(node.Children) {
			return unsupported("/item/summary", "reasoning completion removed a summary")
		}
		before, _ := stringValue(old.Payload)
		after, _ := stringValue(node.Children[index].Payload)
		if !strings.HasPrefix(after, before) || item.summaryDone[index] && after != before {
			return unsupported("/item/summary", "completed reasoning summary changed")
		}
	}
	return nil
}

func (stream *streamModule) encodeVisibleReasoningParts(item *streamItem) ([]p.Value, error) {
	if stream.name != Responses {
		return nil, unsupported("/reasoningContent", "structured reasoning requires a target projection")
	}
	if len(item.node.ReasoningContent) < len(item.visibleTexts) {
		return nil, fmt.Errorf("reasoning content removed an emitted part")
	}
	var frames []p.Value
	for index, part := range item.node.ReasoningContent {
		if !p.PlainReasoningTextPart(part) {
			return nil, unsupported("/reasoningContent", "reasoning content extensions require native frame preservation or an explicit mapping")
		}
		text, _ := stringValue(part.Payload)
		if index == len(item.visibleTexts) {
			item.visibleTexts = append(item.visibleTexts, "")
			frames = append(frames, stream.visibleEvent("response.content_part.added", item, index, "part", object(p.Object{"type": p.StringValue("reasoning_text"), "text": p.StringValue("")})))
		}
		previous := item.visibleTexts[index]
		if !strings.HasPrefix(text, previous) {
			return nil, fmt.Errorf("reasoning content rewrote emitted text")
		}
		if len(text) > len(previous) {
			frames = append(frames, stream.visibleEvent("response.reasoning_text.delta", item, index, "delta", p.StringValue(text[len(previous):])))
		}
		item.visibleTexts[index] = text
	}
	return frames, nil
}

func (stream *streamModule) visibleEvent(kind string, item *streamItem, index int, field string, value p.Value) p.Value {
	fields, _ := stream.responsesEvent(kind, "", item, field, value).ReadObject()
	fields["content_index"], _ = p.EncodeValue(index)
	return object(fields)
}
