package builtin

import (
	"crypto/sha256"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

// Messages own their metadata and child order independently of content. Only
// digests are retained for finished text, not a second text buffer.
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
	key := fmt.Sprintf("output:%d", index)
	parent := stream.items[key]
	if parent == nil || parent.node.Kind != p.MessageNode {
		return nil, unsupported("/output_index", "message completion has no start")
	}
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
			if !sameCompletedPartMetadata(item.node.Metadata, parts[i].Metadata) {
				return nil, unsupported("/item/content", "completed message part metadata changed after content_part.done")
			}
		}
	}
	if !fields["role"].IsZero() && fields["role"] != parent.node.Role {
		return nil, unsupported("/item/role", "message role changed")
	}
	if m.finished {
		if !fields["status"].IsZero() && fields["status"] != parent.node.Status {
			return nil, unsupported("/item/status", "completed message status changed")
		}
		return nil, nil
	}
	node := parent.node
	node.Metadata, node.Status = m.metadata, fields["status"]
	event, err := stream.itemEvent(p.ItemFinished, key, &node, p.Value{})
	if err != nil {
		return nil, err
	}
	events = append(events, event)
	m.finished = true
	return events, nil
}

func sameCompletedPartMetadata(left, right []p.ResponseMetadata) bool {
	canonical := func(entries []p.ResponseMetadata) p.Value {
		values := map[string]any{}
		for _, m := range entries {
			var value any
			if m.Value.Decode(&value) != nil {
				return p.Value{}
			}
			values[m.Codec+"/"+m.Location+"/"+m.Name] = value
		}
		v, _ := p.EncodeValue(values)
		return v
	}
	return canonical(left) == canonical(right)
}
