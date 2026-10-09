package builtin

import (
	"crypto/sha256"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

// Content text can finish before the enclosing message's phase arrives. Keep
// the semantic item open until the message snapshot closes its metadata too.
// Only digests are retained for already finished text, not a second text buffer.
type responseMessageState struct {
	metadata []p.ResponseMetadata
	parts    []string
	finished bool
	bytes    int
}

func (stream *streamModule) responseMessage(index int) (*responseMessageState, error) {
	if m := stream.responseMessages[index]; m != nil {
		return m, nil
	}
	if len(stream.responseMessages) >= stream.limits.StateItems {
		return nil, unsupported("/output_index", "message state exceeds stream item limit")
	}
	if stream.responseMessages == nil {
		stream.responseMessages = map[int]*responseMessageState{}
	}
	m := &responseMessageState{}
	stream.responseMessages[index] = m
	return m, nil
}

func (stream *streamModule) updateMessageMetadata(index int, fields p.Object) (*responseMessageState, error) {
	m, err := stream.responseMessage(index)
	if err != nil {
		return nil, err
	}
	metadata, err := stream.module.extractMetadata(fields, "message", fmt.Sprintf("/output/%d", index))
	if err != nil {
		return nil, err
	}
	merged := p.MergeNodeMetadata(m.metadata, metadata, false)
	if m.finished {
		before, _ := p.EncodeValue(m.metadata)
		after, _ := p.EncodeValue(merged)
		if string(before.Bytes()) != string(after.Bytes()) {
			return nil, unsupported(fmt.Sprintf("/output/%d/phase", index), "completed message metadata changed")
		}
	}
	value, _ := p.EncodeValue(merged)
	size := len(value.Bytes())
	if stream.buffered+size-m.bytes > stream.limits.BufferBytes {
		return nil, unsupported("/item", "message metadata exceeds stream buffer limit")
	}
	stream.buffered += size - m.bytes
	m.metadata, m.bytes = merged, size
	return m, nil
}

func responsePartText(node p.Node) (string, error) {
	text, err := stringValue(node.Payload)
	if err != nil {
		return "", reasoningInputError("/part", "message content requires string text")
	}
	return text, nil
}

func (stream *streamModule) finishResponseMessage(index int, fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	m, err := stream.updateMessageMetadata(index, fields)
	if err != nil {
		return nil, err
	}
	var parts []p.Node
	if !fields["content"].IsZero() {
		parts, err = stream.module.decodeContent(fields["content"], fmt.Sprintf("/output/%d/content", index), p.DecodeResponse, options, &historyState{})
		if err != nil {
			return nil, err
		}
		if len(parts) != len(m.parts) {
			return nil, unsupported("/item/content", "message snapshot does not match its started content parts")
		}
	}
	var events []p.Event
	if len(m.parts) == 0 && !m.finished {
		node := p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), ID: stream.responseItemIDs[index], Metadata: m.metadata}
		key := fmt.Sprintf("output:%d", index)
		for _, kind := range []p.EventType{p.ItemStarted, p.ItemFinished} {
			event, err := stream.itemEvent(kind, key, &node, p.Value{})
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	for i, key := range m.parts {
		item := stream.items[key]
		if !item.partClosed {
			return nil, unsupported("/item/content", "message finished before its content part")
		}
		node := item.node
		if parts != nil {
			if parts[i].Kind != node.Kind {
				return nil, unsupported("/item/content", "completed message part changed kind")
			}
			text, err := responsePartText(parts[i])
			if err != nil {
				return nil, err
			}
			if sha256.Sum256([]byte(text)) != item.closedTextDigest {
				return nil, unsupported("/item/content", "completed message text changed")
			}
			node = parts[i]
		}
		node.Metadata = p.MergeNodeMetadata(node.Metadata, m.metadata, false)
		if m.finished {
			continue // The terminal snapshot still has to agree with closed text.
		}
		e, err := stream.itemEvent(p.ItemFinished, key, &node, p.Value{})
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	m.finished = true
	return events, nil
}
