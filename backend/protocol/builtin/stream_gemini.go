package builtin

import (
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeGeminiFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if failure := fields["error"]; !failure.IsZero() {
		return stream.decodeFailureEvent(failure, options)
	}
	candidates, err := readArray(fields["candidates"])
	if err != nil {
		return nil, err
	}
	var events []p.Event
	if len(candidates) > 0 {
		events = stream.beginResponse(fields["responseId"], fields["modelVersion"])
	}
	for _, value := range candidates {
		candidate, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		index, err := frameIndex(candidate["index"])
		if err != nil {
			return nil, err
		}
		if index != 0 {
			return nil, unsupported("/candidates/index", "multi-candidate streams require an explicit choice lifecycle")
		}
		content, err := nestedObject(candidate, "content")
		if err != nil {
			return nil, err
		}
		if role := content["role"]; !role.IsZero() {
			text, err := stringValue(role)
			if err != nil || text != "model" {
				return nil, unsupported("/candidates/0/content/role", "Gemini output role must be model")
			}
		}
		parts, err := readArray(content["parts"])
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			partFields, err := part.ReadObject()
			if err != nil {
				return nil, err
			}
			// A terminal empty text delta is a framing placeholder, not a new
			// assistant item. Creating one breaks the following tool-result turn.
			// Signed or annotated parts, and empty parts before terminal, retain
			// their identity because a later signature may belong to them.
			if len(partFields) == 1 && partFields["text"].IsString("") && !candidate["finishReason"].IsZero() && !candidate["finishReason"].IsNull() {
				continue
			}
			// A signature-only part applies only to the immediately preceding
			// open part. Never associate by tool name or an arbitrary index.
			if len(partFields) == 1 && !partFields["thoughtSignature"].IsZero() {
				item := stream.items[stream.geminiLastKey]
				if item == nil || item.isFinished {
					return nil, unsupported("/parts/thoughtSignature", "signature has no open part association")
				}
				synthetic := p.Object{"text": p.StringValue(""), "thoughtSignature": partFields["thoughtSignature"]}
				signed, err := stream.module.decodeBlock(object(synthetic), "/parts", p.DecodeResponse, options, &historyState{})
				if err != nil {
					return nil, err
				}
				node := item.node
				node.Resources = signed.Resources
				event, err := stream.itemEvent(p.ItemSnapshot, stream.geminiLastKey, &node, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
				continue
			}
			history := &historyState{calls: map[string][]p.Value{}, next: stream.nextTool}
			node, err := stream.module.decodeBlock(part, "/parts", p.DecodeResponse, options, history)
			if err != nil {
				return nil, err
			}
			stream.nextTool = history.next
			switch node.Kind {
			case p.TextNode, p.ReasoningNode:
				key := stream.geminiLastKey
				previous := stream.items[key]
				if previous == nil || previous.node.Kind != node.Kind || p.HasSignature(previous.node) {
					key = string(node.Kind)
					if _, exists := stream.items[key]; exists {
						key = fmt.Sprintf("%s:%d", node.Kind, stream.geminiPartSerial)
					}
					stream.geminiPartSerial++
				}
				stream.geminiLastKey = key
				batch, err := stream.textEvents(key, node.Kind, node.Payload)
				if err != nil {
					return nil, err
				}
				events = append(events, batch...)
				if len(node.Resources) > 0 {
					node.Payload = p.Value{}
					event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
					if err != nil {
						return nil, err
					}
					events = append(events, event)
				}
			case p.ToolCallNode:
				id, err := stringValue(node.CallID)
				if err != nil {
					return nil, err
				}
				key := "tool:" + id
				stream.geminiLastKey = key
				kind := p.ItemStarted
				if previous := stream.items[key]; previous != nil {
					kind = p.ItemSnapshot
				}
				event, err := stream.itemEvent(kind, key, &node, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
			default:
				return nil, unsupported("/parts", "Gemini stream part requires a supported typed event mapping")
			}
		}
		if finish := candidate["finishReason"]; !finish.IsZero() && !finish.IsNull() {
			for _, key := range stream.order {
				item := stream.items[key]
				if item.node.Kind == p.ToolCallNode && !item.isFinished {
					event, err := stream.itemEvent(p.ItemFinished, key, nil, p.Value{})
					if err != nil {
						return nil, err
					}
					events = append(events, event)
				}
			}
			stream.finish, err = decodeFinishReason(Gemini, finish)
			if err != nil {
				return nil, err
			}
			if string(stream.finish.Bytes()) == `"stop"` {
				for _, item := range stream.items {
					if item.node.Kind == p.ToolCallNode {
						stream.finish = p.StringValue("tool_calls")
						break
					}
				}
			}
			stream.isFinished = true
			events = append(events, p.Event{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": stream.finish}}})
		}
	}
	usage, err := stream.usageEvent(fields["usageMetadata"])
	if err != nil {
		return nil, err
	}
	return append(events, usage...), nil
}
