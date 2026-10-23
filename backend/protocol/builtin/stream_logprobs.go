package builtin

import p "github.com/elysia-api/backend/protocol"

type streamLogprobs struct {
	index    int
	metadata p.ResponseMetadata
	deltas   []p.Value
	bytes    int
}

func (stream *streamModule) deferLogprobDelta(item *streamItem, event p.Event) bool {
	if event.Type != p.ItemDelta || event.Item != nil || !event.CallID.IsZero() || len(event.Metadata) != 1 {
		return false
	}
	metadata := event.Metadata[0]
	if metadata.Name != "logprobs" || metadata.Codec != stream.name || (stream.name != Chat || metadata.Location != "choice") && (stream.name != Responses || metadata.Location != "content") {
		return false
	}
	hasContainer := func(value p.Value) bool {
		if value.IsNull() {
			return true
		}
		if stream.name == Responses {
			return value.IsArray()
		}
		fields, err := value.ReadObject()
		if err != nil {
			return false
		}
		for _, field := range fields {
			if !field.IsArray() && !field.IsNull() {
				return false
			}
		}
		return true
	}
	if !hasContainer(metadata.Value) {
		return false
	}
	index := -1
	for position, current := range item.node.Metadata {
		if current.Codec == metadata.Codec && current.Location == metadata.Location && current.Name == metadata.Name {
			index = position
			break
		}
	}
	if index < 0 || item.logprobs != nil && item.logprobs.index != index || item.logprobs == nil && !hasContainer(item.node.Metadata[index].Value) {
		return false
	}
	if stream.logprobItems == stream.limits.StateItems || metadata.Value.Len() > stream.limits.BufferBytes-stream.logprobBytes {
		stream.materializeLogprobs()
	}
	if metadata.Value.Len() > stream.limits.BufferBytes {
		return false
	}
	if item.logprobs == nil {
		item.logprobs = &streamLogprobs{index: index}
	}
	pending := item.logprobs
	pending.metadata = metadata
	pending.deltas = append(pending.deltas, metadata.Value)
	pending.bytes += metadata.Value.Len()
	stream.logprobItems++
	stream.logprobBytes += metadata.Value.Len()
	return true
}

func (stream *streamModule) materializeLogprob(item *streamItem) {
	pending := item.logprobs
	if pending == nil {
		return
	}
	metadata := pending.metadata
	metadata.Value = p.MergeLogprobDeltas(item.node.Metadata[pending.index].Value, pending.deltas, metadata.Codec)
	item.node.Metadata = p.MergeNodeMetadata(item.node.Metadata, []p.ResponseMetadata{metadata}, false)
	stream.logprobItems -= len(pending.deltas)
	stream.logprobBytes -= pending.bytes
	item.logprobs = nil
}

func (stream *streamModule) materializeLogprobs() {
	if stream.logprobItems == 0 {
		return
	}
	for _, item := range stream.items {
		stream.materializeLogprob(item)
	}
}
