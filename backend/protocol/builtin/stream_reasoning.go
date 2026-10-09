package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeSummaryFrame(kind, key string, fields p.Object) ([]p.Event, error) {
	item := stream.items[key]
	if item == nil || (item.node.ReasoningForm != p.SummaryReasoning && item.node.ReasoningForm != p.StructuredReasoning) || item.isFinished {
		return nil, fmt.Errorf("reasoning summary has no active reasoning item")
	}
	index, err := frameIndex(fields["summary_index"])
	if err != nil {
		return nil, err
	}
	node := item.node
	node.Children = append([]p.Node(nil), node.Children...)
	if kind == "response.reasoning_summary_part.added" {
		if index != len(node.Children) {
			return nil, fmt.Errorf("summary part index is not the next ordered part")
		}
		part, err := fields["part"].ReadObject()
		if err != nil {
			return nil, err
		}
		if part["type"] != p.StringValue("summary_text") {
			return nil, unsupported("/part/type", "unsupported reasoning summary part")
		}
		if _, err := stringValue(part["text"]); err != nil {
			return nil, err
		}
		node.Children = append(node.Children, p.Node{Kind: p.TextNode, Payload: part["text"], Attributes: stream.module.extensions(part, []string{"type", "text"})})
	} else {
		if index >= len(node.Children) {
			return nil, fmt.Errorf("summary update has no preceding part")
		}
		if item.summaryDone[index] {
			return nil, fmt.Errorf("summary received content after part completion")
		}
		if item.summaryTextDone[index] && kind != "response.reasoning_summary_part.done" {
			return nil, fmt.Errorf("summary text received content after text completion")
		}
		previous, err := stringValue(node.Children[index].Payload)
		if err != nil {
			return nil, err
		}
		value := fields["text"]
		if kind == "response.reasoning_summary_text.delta" {
			value = fields["delta"]
		}
		if kind == "response.reasoning_summary_part.done" {
			part, err := fields["part"].ReadObject()
			if err != nil {
				return nil, err
			}
			if part["type"] != p.StringValue("summary_text") {
				return nil, unsupported("/part/type", "unsupported reasoning summary part")
			}
			value = part["text"]
			node.Children[index].Attributes = stream.module.extensions(part, []string{"type", "text"})
			if item.summaryDone == nil {
				item.summaryDone = map[int]bool{}
			}
			item.summaryDone[index] = true
		}
		text, err := stringValue(value)
		if err != nil {
			return nil, err
		}
		if kind == "response.reasoning_summary_text.delta" {
			text = previous + text
		} else if !strings.HasPrefix(text, previous) {
			return nil, fmt.Errorf("reasoning summary snapshot rewrote emitted text")
		}
		if item.summaryTextDone[index] && text != previous {
			return nil, fmt.Errorf("completed reasoning text changed in the part snapshot")
		}
		if kind == "response.reasoning_summary_text.done" {
			if item.summaryTextDone == nil {
				item.summaryTextDone = map[int]bool{}
			}
			item.summaryTextDone[index] = true
		}
		node.Children[index].Payload = p.StringValue(text)
	}
	event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
	return []p.Event{event}, err
}

func (stream *streamModule) encodeSummaryParts(item *streamItem) ([]p.Value, error) {
	if stream.name != Responses {
		return nil, unsupported("/reasoningForm", "target cannot express reasoning summaries")
	}
	if len(item.node.Children) < len(item.summaryTexts) {
		return nil, fmt.Errorf("summary removed an emitted part")
	}
	var frames []p.Value
	for index, child := range item.node.Children {
		if !p.PlainReasoningTextPart(child) {
			return nil, unsupported("/summary", "extended summary parts require native frame preservation or an explicit mapping")
		}
		text, err := stringValue(child.Payload)
		if err != nil {
			return nil, err
		}
		if len(child.Attributes) > 0 {
			return nil, unsupported("/summary/attributes", "summary extensions require original frame preservation")
		}
		if index == len(item.summaryTexts) {
			item.summaryTexts = append(item.summaryTexts, "")
			frames = append(frames, stream.summaryEvent("response.reasoning_summary_part.added", item, index, "part", object(p.Object{"type": p.StringValue("summary_text"), "text": p.StringValue("")})))
		}
		previous := item.summaryTexts[index]
		if !strings.HasPrefix(text, previous) {
			return nil, fmt.Errorf("summary snapshot rewrote emitted text")
		}
		if len(text) > len(previous) {
			frames = append(frames, stream.summaryEvent("response.reasoning_summary_text.delta", item, index, "delta", p.StringValue(text[len(previous):])))
		}
		item.summaryTexts[index] = text
	}
	return frames, nil
}

func (stream *streamModule) summaryEvent(kind string, item *streamItem, index int, field string, value p.Value) p.Value {
	fields, _ := stream.responsesEvent(kind, "", item, field, value).ReadObject()
	delete(fields, "content_index")
	fields["summary_index"], _ = p.EncodeValue(index)
	return object(fields)
}
