package protocol

type collectedLogprobs struct {
	index    int
	metadata ResponseMetadata
	deltas   []Value
	bytes    int
}

func (collector *ResponseCollector) deferLogprobDelta(key string, event Event) bool {
	if event.Type != ItemDelta || event.Item != nil || !event.CallID.IsZero() || len(event.Metadata) != 1 {
		return false
	}
	metadata := event.Metadata[0]
	if metadata.Name != "logprobs" || (metadata.Codec != "openai-chat" || metadata.Location != "choice") && (metadata.Codec != "responses" || metadata.Location != "content") || !canDeferLogprobValue(metadata.Value, metadata.Codec) {
		return false
	}
	index := -1
	for position, current := range collector.item(key).Metadata {
		if current.Codec == metadata.Codec && current.Location == metadata.Location && current.Name == metadata.Name {
			index = position
			break
		}
	}
	if index < 0 {
		return false // The first value retains its exact original spelling.
	}
	pending := collector.logprobs[key]
	if pending != nil && pending.index != index {
		return false
	}
	if pending == nil && !canDeferLogprobValue(collector.item(key).Metadata[index].Value, metadata.Codec) {
		return false
	}
	// Retained fragments have separate bounded storage. Reaching the bound
	// materializes existing work; it never introduces a new request rejection.
	if collector.logprobItems == collector.logprobLimit || metadata.Value.Len() > collector.limit-collector.logprobBytes {
		collector.materializeLogprobs()
		pending = nil
	}
	if metadata.Value.Len() > collector.limit {
		return false
	}
	if pending == nil {
		if collector.logprobs == nil {
			collector.logprobs = make(map[string]*collectedLogprobs)
		}
		pending = &collectedLogprobs{index: index}
		collector.logprobs[key] = pending
	}
	pending.metadata = metadata
	pending.deltas = append(pending.deltas, metadata.Value)
	pending.bytes += metadata.Value.Len()
	collector.logprobItems++
	collector.logprobBytes += metadata.Value.Len()
	return true
}

func (collector *ResponseCollector) materializeLogprob(key string) {
	pending := collector.logprobs[key]
	if pending == nil {
		return
	}
	item := collector.item(key)
	metadata := pending.metadata
	metadata.Value = MergeLogprobDeltas(item.Metadata[pending.index].Value, pending.deltas, metadata.Codec)
	item.Metadata = MergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata}, false)
	collector.logprobItems -= len(pending.deltas)
	collector.logprobBytes -= pending.bytes
	delete(collector.logprobs, key)
}

func (collector *ResponseCollector) materializeLogprobs() {
	for key := range collector.logprobs {
		collector.materializeLogprob(key)
	}
}
