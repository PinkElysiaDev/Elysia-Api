package protocol

import "fmt"

type collectedMetadata struct {
	index         int
	metadata      ResponseMetadata
	deltas        []Value
	bytes         int
	charged       int
	hasArrayItems bool
}

func canDeferMetadata(metadata ResponseMetadata) bool {
	if metadata.Update == "append" {
		return metadata.Value.IsArray()
	}
	return metadata.Name == "logprobs" && ((metadata.Codec == "openai-chat" && metadata.Location == "choice") || (metadata.Codec == "responses" && metadata.Location == "content")) && canDeferLogprobValue(metadata.Value, metadata.Codec)
}

func metadataPosition(items []ResponseMetadata, metadata ResponseMetadata) int {
	for index, current := range items {
		if current.Codec == metadata.Codec && current.Location == metadata.Location && current.Name == metadata.Name {
			return index
		}
	}
	return -1
}

func (collector *ResponseCollector) deferMetadataDelta(key string, event Event) (bool, error) {
	if event.Type != ItemDelta || event.Item != nil || !event.CallID.IsZero() || len(event.Metadata) == 0 {
		return false, nil
	}
	for _, metadata := range event.Metadata {
		if !canDeferMetadata(metadata) {
			return false, nil
		}
		position := metadataPosition(collector.item(key).Metadata, metadata)
		if position >= 0 {
			// A pending batch holds immutable fragments. Its initial snapshot
			// already passed the same shape check before the batch was created.
			if pending := collector.pendingMetadata(key, position); pending != nil && len(pending.deltas) != 0 && pending.metadata.Update == metadata.Update {
				continue
			}
			current := collector.item(key).Metadata[position].Value
			if metadata.Update == "append" && !current.IsArray() {
				return false, streamIssue(InvalidInput, metadata.Path, "array metadata cannot append to a non-array snapshot")
			}
			if metadata.Update == "" && !canDeferLogprobValue(current, metadata.Codec) {
				return false, nil
			}
		}
	}
	for _, metadata := range event.Metadata {
		if err := collector.appendMetadataDelta(key, metadata); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (collector *ResponseCollector) pendingMetadata(key string, position int) *collectedMetadata {
	for _, pending := range collector.metadataDeltas[key] {
		if pending.index == position {
			return pending
		}
	}
	return nil
}

func (collector *ResponseCollector) appendMetadataDelta(key string, metadata ResponseMetadata) error {
	item := collector.item(key)
	position := metadataPosition(item.Metadata, metadata)
	if position < 0 {
		metadata.Update = ""
		item.Metadata = append(item.Metadata, metadata)
		return collector.accountItemMetadata(key, *item)
	}
	// Bound retained fragments independently. Materialization preserves the
	// snapshot instead of rejecting solely because it arrived in many frames.
	if collector.metadataItems+collector.retainedItems() >= collector.replay.limits.StateItems || metadata.Value.Len() > collector.limit-collector.metadataBytes {
		collector.materializeMetadata()
	}
	if metadata.Value.Len() > collector.limit {
		return streamIssue(LimitExceeded, metadata.Path, "metadata delta exceeds the collection limit")
	}
	pending := collector.pendingMetadata(key, position)
	if pending == nil {
		if collector.metadataDeltas == nil {
			collector.metadataDeltas = make(map[string][]*collectedMetadata)
		}
		pending = &collectedMetadata{index: position}
		if metadata.Update == "append" {
			pending.hasArrayItems = hasMetadataArrayItems(item.Metadata[position].Value)
		}
		collector.metadataDeltas[key] = append(collector.metadataDeltas[key], pending)
	}
	if len(pending.deltas) != 0 && pending.metadata.Update != metadata.Update {
		// The explicit append contract and legacy probability contract cannot
		// share a pending batch: either may have a different container shape.
		collector.materializeItemMetadata(key)
		return collector.appendMetadataDelta(key, metadata)
	}
	previous := item.Metadata[position]
	if len(pending.deltas) != 0 {
		previous = pending.metadata
	}
	meter := typedMeter{limits: collector.replay.limits}
	if !meter.raw(metadata.Value.raw) {
		return streamIssue(LimitExceeded, metadata.Path, "metadata delta exceeds the collection limit")
	}
	charge := meter.bytes
	if metadata.Update == "append" {
		charge -= 2 // The array brackets belong to the existing snapshot.
		hasItems := hasMetadataArrayItems(metadata.Value)
		if hasItems && pending.hasArrayItems {
			charge++
		}
		pending.hasArrayItems = pending.hasArrayItems || hasItems
	}
	charge += jsonStringBytes(metadata.Path) + jsonStringBytes(metadata.SourceCodec) - jsonStringBytes(previous.Path) - jsonStringBytes(previous.SourceCodec)
	if charge > collector.limit-collector.bytes-collector.replay.retainedBytes() {
		if metadata.Update == "append" {
			return streamIssue(LimitExceeded, metadata.Path, "appended metadata exceeds the collection limit")
		}
		// Legacy fragments retain container syntax until materialization. At
		// the boundary, charge the merged snapshot so frame count or whitespace
		// cannot reject content that fits in the same collection budget.
		collector.materializeMetadata()
		item.Metadata = MergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata}, true)
		return collector.accountItemMetadata(key, *item)
	}
	collector.bytes += charge
	pending.charged += charge
	pending.metadata = metadata
	pending.deltas = append(pending.deltas, metadata.Value)
	pending.bytes += metadata.Value.Len()
	collector.metadataItems++
	collector.metadataBytes += metadata.Value.Len()
	return nil
}

func (collector *ResponseCollector) materializeItemMetadata(key string) {
	item := collector.item(key)
	for _, pending := range collector.metadataDeltas[key] {
		if len(pending.deltas) == 0 {
			continue
		}
		metadata := pending.metadata
		if metadata.Update == "append" {
			// Shape was checked before these immutable array fragments entered
			// the batch; EncodeValue only contains already valid JSON values.
			metadata.Value, _ = mergeArrayDeltas(item.Metadata[pending.index].Value, pending.deltas)
			metadata.Update = ""
		} else {
			previous := item.Metadata[pending.index]
			metadata.Value = MergeLogprobDeltas(previous.Value, pending.deltas, metadata.Codec)
			// The merger already returned normalized JSON. Only the value and
			// diagnostic strings can differ within this metadata identity.
			charge := metadata.Value.Len() - len(compactValueString(previous.Value.raw))
			charge += jsonStringBytes(metadata.Path) + jsonStringBytes(metadata.SourceCodec) - jsonStringBytes(previous.Path) - jsonStringBytes(previous.SourceCodec)
			collector.bytes += charge - pending.charged
			pending.charged = charge
		}
		item.Metadata = MergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata}, false)
		// Transfer the already charged fragments to this item's snapshot ledger.
		// A later metadata replacement recomputes its exact encoded size once.
		collector.metadata[key] += pending.charged
		collector.metadataItems -= len(pending.deltas)
		collector.metadataBytes -= pending.bytes
	}
	delete(collector.metadataDeltas, key)
}

func (collector *ResponseCollector) materializeMetadata() {
	for key := range collector.metadataDeltas {
		collector.materializeItemMetadata(key)
	}
}

func (collector *ResponseCollector) mergeEventMetadata(key string, event Event) error {
	item := collector.item(key)
	for _, metadata := range event.Metadata {
		if metadata.Update == "append" {
			position := metadataPosition(item.Metadata, metadata)
			if position >= 0 {
				if !item.Metadata[position].Value.IsArray() {
					return streamIssue(InvalidInput, metadata.Path, "array metadata cannot append to a non-array snapshot")
				}
				value, err := mergeArrayDeltas(item.Metadata[position].Value, []Value{metadata.Value})
				if err != nil {
					return fmt.Errorf("merge item metadata: %w", err)
				}
				metadata.Value = value
			}
			metadata.Update = ""
			item.Metadata = MergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata}, false)
		} else {
			item.Metadata = MergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata}, event.Type == ItemDelta)
		}
	}
	return nil
}

func hasMetadataArrayItems(value Value) bool {
	start := skipJSONSpace(value.raw, 0)
	return value.raw[skipJSONSpace(value.raw, start+1)] != ']'
}
