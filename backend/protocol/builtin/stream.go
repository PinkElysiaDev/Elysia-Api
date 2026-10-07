package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

type streamItem struct {
	node            p.Node
	index           int
	isFinished      bool
	text            p.TextTracker
	buffer          strings.Builder
	metadataBytes   int
	wireID          p.Value
	hasEmittedStart bool
	summaryTexts    []string
	summaryDone     map[int]bool
	summaryTextDone map[int]bool
}

type streamModule struct {
	module
	limits          p.Limits
	isStarted       bool
	isFinished      bool
	hasFailed       bool
	id              p.Value
	model           p.Value
	finish          p.Value
	items           map[string]*streamItem
	identities      *p.ItemIdentities
	order           []string
	nextTool        int
	buffered        int
	pending         *p.Event
	usage           *p.Usage
	usageFields     p.Object
	usageBytes      int
	responseItemIDs map[int]p.Value
	attributes      p.Object
	sequence        int64
	hasSequence     bool
}

func (adapter module) NewStream(direction p.Direction, limits p.Limits) (p.Module, error) {
	return &streamModule{module: adapter, limits: limits, items: map[string]*streamItem{}, identities: p.NewItemIdentities(limits.StateItems)}, nil
}

func (stream *streamModule) Convert(ctx context.Context, direction p.Direction, input p.Value, options p.EvaluationContext) (p.Value, error) {
	if err := ctx.Err(); err != nil {
		return p.Value{}, err
	}
	if direction == p.EncodeEvent {
		var event p.Event
		if err := input.Decode(&event); err != nil {
			return p.Value{}, err
		}
		frames, err := stream.encodeEvent(event, options)
		if err != nil {
			return p.Value{}, err
		}
		return array(stream.numberFrames(frames)), nil
	}
	events, err := stream.DecodeEvents(ctx, input, options)
	if err != nil {
		return p.Value{}, err
	}
	return p.EncodeValue(events)
}

func (stream *streamModule) DecodeEvents(ctx context.Context, input p.Value, options p.EvaluationContext) ([]p.Event, error) {
	fields, err := input.ReadObject()
	if err != nil {
		return nil, err
	}
	var events []p.Event
	switch stream.name {
	case Chat:
		events, err = stream.decodeChatFrame(fields, options)
	case Anthropic:
		events, err = stream.decodeAnthropicFrame(fields, options)
	case Responses:
		events, err = stream.decodeResponsesFrame(fields, options)
	case Gemini:
		events, err = stream.decodeGeminiFrame(fields, options)
	}
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []p.Event{}
	}
	extra, err := stream.captureFrameExtensions(fields)
	if err != nil {
		return nil, err
	}
	if !extra.IsZero() {
		if len(events) == 0 {
			events = append(events, p.Event{Type: p.NativeEvent})
		}
		events[0].Unmapped = stream.module.native(extra, "/frame/extensions", p.DecodeEvent, options)
	}
	for index := range events {
		events[index].SchemaVersion = p.SemanticSchemaVersion
		events[index].Source = options.Identity()
		if events[index].ResponseID.IsZero() {
			events[index].ResponseID = stream.id
		}
	}
	return events, nil
}

func (stream *streamModule) begin(id, model p.Value) []p.Event {
	if !id.IsZero() {
		stream.id = id
	}
	if !model.IsZero() {
		stream.model = model
	}
	if stream.isStarted {
		return nil
	}
	stream.isStarted = true
	return []p.Event{{Type: p.ResponseStarted, ResponseID: stream.id, Response: &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("in_progress"), Content: []p.Node{}, Attributes: copyFields(stream.attributes)}}}
}

func (stream *streamModule) itemEvent(kind p.EventType, key string, node *p.Node, delta p.Value) (p.Event, error) {
	item := stream.items[key]
	if kind == p.ItemStarted {
		if item != nil {
			return p.Event{}, fmt.Errorf("stream item %q started twice", key)
		}
		if len(stream.items) >= stream.limits.StateItems {
			return p.Event{}, unsupported("/items", "stream item limit exceeded")
		}
		item = &streamItem{index: len(stream.order)}
		stream.items[key] = item
		stream.order = append(stream.order, key)
	} else if item == nil {
		return p.Event{}, fmt.Errorf("stream item %q has no start", key)
	}
	if item.isFinished {
		return p.Event{}, fmt.Errorf("stream item %q received content after completion", key)
	}
	if node != nil {
		if err := stream.storeItemMetadata(item, *node); err != nil {
			return p.Event{}, err
		}
	}
	if kind == p.ItemFinished {
		item.isFinished = true
	}
	return p.Event{Type: kind, ItemID: p.StringValue(key), Index: &item.index, Item: node, Delta: delta}, nil
}

func itemMetadata(node p.Node) p.Node {
	node.Payload = p.Value{}
	if node.Input != nil {
		node.Input = &p.ToolInput{Kind: node.Input.Kind}
	}
	if node.Native != nil {
		origin := node.Native.Source
		node.Source, node.Native = &origin, nil
	}
	return node
}

func (stream *streamModule) storeItemMetadata(item *streamItem, node p.Node) error {
	metadata := itemMetadata(node)
	value, err := p.EncodeValue(metadata)
	if err != nil {
		return err
	}
	size := len(value.Bytes())
	buffered := stream.buffered + size - item.metadataBytes
	if buffered > stream.limits.BufferBytes {
		return unsupported("/items", "stream metadata exceeds its retained state limit")
	}
	stream.buffered, item.metadataBytes, item.node = buffered, size, metadata
	return nil
}

func (stream *streamModule) textEvents(key string, kind p.NodeKind, value p.Value) ([]p.Event, error) {
	var events []p.Event
	if _, exists := stream.items[key]; !exists {
		event, err := stream.itemEvent(p.ItemStarted, key, &p.Node{Kind: kind}, p.Value{})
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	event, err := stream.itemEvent(p.ItemDelta, key, nil, value)
	if err != nil {
		return nil, err
	}
	return append(events, event), nil
}

func (stream *streamModule) usageEvent(value p.Value) ([]p.Event, error) {
	usage, err := stream.decodeUsageUpdate(value)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, nil
	}
	return []p.Event{{Type: p.UsageUpdated, Usage: usage}}, nil
}

func frameIndex(value p.Value) (int, error) {
	var index int
	if value.IsZero() {
		return 0, nil
	} // Wire protocols define omitted choice index as zero.
	if err := value.Decode(&index); err != nil || index < 0 {
		return 0, fmt.Errorf("invalid stream index")
	}
	return index, nil
}

func (stream *streamModule) decodeChatFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if failure := fields["error"]; !failure.IsZero() {
		return stream.decodeFailureEvent(failure, options)
	}
	choices, err := readArray(fields["choices"])
	if err != nil {
		return nil, err
	}
	var events []p.Event
	if len(choices) > 0 {
		if created := fields["created"]; !created.IsZero() {
			stream.attributes = mergeAttributes(stream.attributes, p.Object{"created_at": created})
		}
		events = stream.begin(fields["id"], fields["model"])
	}
	for _, value := range choices {
		choice, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		index, err := frameIndex(choice["index"])
		if err != nil {
			return nil, err
		}
		if index != 0 {
			return nil, unsupported("/choices/index", "multi-choice streaming requires a declared choice lifecycle")
		}
		delta, err := nestedObject(choice, "delta")
		if err != nil {
			return nil, err
		}
		for _, part := range []struct {
			field, key string
			kind       p.NodeKind
		}{{"reasoning_content", "reasoning", p.ReasoningNode}, {"content", "text", p.TextNode}, {"refusal", "refusal", p.RefusalNode}} {
			if content := delta[part.field]; !content.IsZero() && !content.IsNull() {
				batch, err := stream.textEvents(part.key, part.kind, content)
				if err != nil {
					return nil, err
				}
				events = append(events, batch...)
			}
		}
		calls, err := readArray(delta["tool_calls"])
		if err != nil {
			return nil, err
		}
		for _, value := range calls {
			call, err := value.ReadObject()
			if err != nil {
				return nil, err
			}
			position, err := frameIndex(call["index"])
			if err != nil {
				return nil, err
			}
			key := "tool:" + strconv.Itoa(position)
			function, err := nestedObject(call, "function")
			if err != nil {
				return nil, err
			}
			item := stream.items[key]
			node := p.Node{Kind: p.ToolCallNode, Input: &p.ToolInput{Kind: p.JSONInput}}
			kind := p.ItemStarted
			if item != nil {
				node = item.node
				kind = p.ItemSnapshot
			}
			if !call["id"].IsZero() {
				node.CallID = call["id"]
			}
			if !function["name"].IsZero() {
				node.Name = function["name"]
			}
			event, err := stream.itemEvent(kind, key, &node, p.Value{})
			if err != nil {
				return nil, err
			}
			events = append(events, event)
			if arguments := function["arguments"]; !arguments.IsZero() {
				event, err := stream.itemEvent(p.ItemDelta, key, nil, arguments)
				if err != nil {
					return nil, err
				}
				events = append(events, event)
			}
		}
		if finish := choice["finish_reason"]; !finish.IsZero() && !finish.IsNull() {
			stream.finish, err = decodeFinishReason(Chat, finish)
			if err != nil {
				return nil, err
			}
			stream.isFinished = true
			attributes := mergeAttributes(copyFields(stream.attributes), p.Object{"finishReason": stream.finish})
			events = append(events, p.Event{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Attributes: attributes}})
		}
	}
	usage, err := stream.usageEvent(fields["usage"])
	if err != nil {
		return nil, err
	}
	return append(events, usage...), nil
}

func (stream *streamModule) decodeAnthropicFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	kind, err := stringValue(fields["type"])
	if err != nil {
		return nil, err
	}
	index, err := frameIndex(fields["index"])
	if err != nil {
		return nil, err
	}
	key := "item:" + strconv.Itoa(index)
	switch kind {
	case "ping":
		return nil, nil
	case "error":
		return stream.decodeFailureEvent(fields["error"], options)
	case "message_start":
		message, err := fields["message"].ReadObject()
		if err != nil {
			return nil, err
		}
		events := stream.begin(message["id"], message["model"])
		usage, err := stream.usageEvent(message["usage"])
		return append(events, usage...), err
	case "content_block_start":
		node, err := stream.module.decodeBlock(fields["content_block"], "/content_block", p.DecodeResponse, options, &historyState{calls: map[string][]p.Value{}})
		if err != nil {
			return nil, err
		}
		if node.Kind == p.ToolCallNode && node.Input.Value.IsObject() {
			object, err := node.Input.Value.ReadObject()
			if err != nil {
				return nil, err
			}
			if len(object) == 0 {
				node.Input = &p.ToolInput{Kind: p.JSONInput}
			}
		}
		event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
		return []p.Event{event}, err
	case "content_block_delta":
		delta, err := fields["delta"].ReadObject()
		if err != nil {
			return nil, err
		}
		deltaKind, err := stringValue(delta["type"])
		if err != nil {
			return nil, err
		}
		field := map[string]string{"text_delta": "text", "thinking_delta": "thinking", "input_json_delta": "partial_json"}[deltaKind]
		if field != "" {
			event, err := stream.itemEvent(p.ItemDelta, key, nil, delta[field])
			return []p.Event{event}, err
		}
		if deltaKind == "signature_delta" {
			item := stream.items[key]
			if item == nil {
				return nil, fmt.Errorf("signature has no content item")
			}
			node := item.node
			node.Payload = p.Value{}
			node.Resources = []p.Resource{{Kind: "signature", ID: delta["signature"], Scope: options.Scope}}
			event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
			return []p.Event{event}, err
		}
		return nil, unsupported("/delta/type", "unsupported Anthropic delta "+deltaKind)
	case "content_block_stop":
		event, err := stream.itemEvent(p.ItemFinished, key, nil, p.Value{})
		return []p.Event{event}, err
	case "message_delta":
		delta, err := nestedObject(fields, "delta")
		if err != nil {
			return nil, err
		}
		if reason := delta["stop_reason"]; !reason.IsZero() {
			stream.finish, err = decodeFinishReason(Anthropic, reason)
			if err != nil {
				return nil, err
			}
		}
		return stream.usageEvent(fields["usage"])
	case "message_stop":
		if stream.finish.IsZero() || stream.finish.IsNull() {
			return nil, unsupported("/stop_reason", "message_stop has no preceding terminal reason")
		}
		stream.isFinished = true
		return []p.Event{{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": stream.finish}}}}, nil
	default:
		return nil, unsupported("/type", "unsupported Anthropic event "+kind)
	}
}
