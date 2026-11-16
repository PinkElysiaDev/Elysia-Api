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
		if metadata.Update != "append" || !metadata.References.IsZero() {
			return false, nil
		}
		position := metadataPosition(collector.item(key).Metadata, metadata)
		if position >= 0 {
			if err := checkMetadataAppend(collector.item(key).Metadata[position], metadata); err != nil {
				return false, err
			}
			// A pending batch holds immutable fragments. Its initial snapshot
			// already passed the same shape check before the batch was created.
			if pending := collector.pendingMetadata(key, position); pending != nil && len(pending.deltas) != 0 {
				continue
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
		pending = &collectedMetadata{index: position, hasArrayItems: hasMetadataArrayItems(item.Metadata[position].Value)}
		collector.metadataDeltas[key] = append(collector.metadataDeltas[key], pending)
	}
	previous := item.Metadata[position]
	if len(pending.deltas) != 0 {
		previous = pending.metadata
	}
	meter := typedMeter{limits: collector.replay.limits}
	if !meter.raw(metadata.Value.raw) {
		return streamIssue(LimitExceeded, metadata.Path, "metadata delta exceeds the collection limit")
	}
	charge := meter.bytes - 2 // The array brackets belong to the existing snapshot.
	hasItems := hasMetadataArrayItems(metadata.Value)
	if hasItems && pending.hasArrayItems {
		charge++
	}
	pending.hasArrayItems = pending.hasArrayItems || hasItems
	charge += jsonStringBytes(metadata.Path) + jsonStringBytes(metadata.SourceCodec) - jsonStringBytes(previous.Path) - jsonStringBytes(previous.SourceCodec)
	if charge > collector.limit-collector.bytes-collector.replay.retainedBytes() {
		return streamIssue(LimitExceeded, metadata.Path, "appended metadata exceeds the collection limit")
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
		// Only validated array fragments enter the batch; materialization cannot
		// introduce a different metadata shape.
		metadata.Value, _ = mergeArrayDeltas(item.Metadata[pending.index].Value, pending.deltas)
		metadata.Update = ""
		item.Metadata = mergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata})
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
				merged, err := appendMetadata(item.Metadata[position], metadata)
				if err != nil {
					return fmt.Errorf("merge item metadata: %w", err)
				}
				metadata = merged
			}
			metadata.Update = ""
		}
		item.Metadata = mergeNodeMetadata(item.Metadata, []ResponseMetadata{metadata})
	}
	return nil
}

func checkMetadataAppend(previous, update ResponseMetadata) error {
	if previous.Meaning != update.Meaning || previous.References.IsZero() != update.References.IsZero() {
		return streamIssue(InvalidInput, update.Path, "metadata append cannot change its declared meaning or reference ownership")
	}
	if !previous.Value.IsArray() {
		return streamIssue(InvalidInput, update.Path, "array metadata cannot append to a non-array snapshot")
	}
	return nil
}

func appendMetadata(previous, update ResponseMetadata) (ResponseMetadata, error) {
	if err := checkMetadataAppend(previous, update); err != nil {
		return ResponseMetadata{}, err
	}
	for _, field := range []struct {
		previous Value
		value    *Value
	}{{previous.Value, &update.Value}, {previous.References, &update.References}} {
		if field.value.IsZero() {
			continue
		}
		merged, err := mergeArrayDeltas(field.previous, []Value{*field.value})
		if err != nil {
			return ResponseMetadata{}, err
		}
		*field.value = merged
	}
	return update, nil
}

func hasMetadataArrayItems(value Value) bool {
	start := skipJSONSpace(value.raw, 0)
	return value.raw[skipJSONSpace(value.raw, start+1)] != ']'
}
