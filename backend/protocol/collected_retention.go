package protocol

// Root fields and complete snapshots share the incremental content budget.
// A plain text delta never visits the accumulated response body.
func (collector *ResponseCollector) setResponse(response Response, isSnapshot bool) error {
	header := response
	header.Content, header.Usage = nil, nil
	// The schema and compiled protocol identity are fixed shared slots, not
	// dynamic response state. Meter semantic structure with its existing shape
	// limits, then apply the original byte budget to the retained fields.
	header.SchemaVersion, header.Source = collector.replay.target.coreSchemaVersion(), collector.replay.target.Protocol
	size, err := measureSemanticValue(&header, semanticLimits(collector.replay.limits))
	if err != nil {
		return err
	}
	headerBytes := size.bytes - collector.headerBaseBytes
	retained := collector.bytes - collector.headerBytes + headerBytes
	snapshotItems := collector.snapshotItems
	if isSnapshot {
		snapshot := response
		snapshot.Usage = nil // Replay owns the merged usage, including later tails.
		complete, err := measureSemanticValue(&snapshot, collector.replay.limits)
		if err != nil {
			return err
		}
		retained = complete.bytes + collector.unmappedBytes
		snapshotItems = countCollectedNodes(response.Content)
		if collector.replay.isTerminal {
			// The terminal was compared with the streamed content. Its nodes
			// replace that content; only additional containers need a new item.
			snapshotItems = max(0, snapshotItems-len(collector.replay.items)-collector.replay.embeddedItems)
		}
	}
	if err := collector.checkRetainedBytes(retained); err != nil {
		return err
	}
	additional := snapshotItems - collector.snapshotItems + len(response.Metadata) + len(response.Attributes) - len(collector.response.Metadata) - len(collector.response.Attributes)
	if err := collector.reserveItems(additional); err != nil {
		return err
	}
	if !isSnapshot {
		retained = collector.bytes - collector.headerBytes + headerBytes
	}
	collector.bytes, collector.headerBytes, collector.snapshotItems = retained, headerBytes, snapshotItems
	collector.response = response
	return nil
}

func countCollectedNodes(nodes []Node) int {
	count := len(nodes)
	for _, node := range nodes {
		count += countCollectedNodes(node.Children) + countCollectedNodes(node.ReasoningContent)
	}
	return count
}

func (collector *ResponseCollector) retainedItems() int {
	return collector.replay.retainedItems() + collector.snapshotItems + len(collector.response.Metadata) + len(collector.response.Attributes) + len(collector.unmapped)
}

func (collector *ResponseCollector) reserveItems(additional int) error {
	items := collector.retainedItems() + additional
	if items > collector.replay.limits.StateItems {
		return streamIssue(LimitExceeded, "/response", "collected content and metadata exceed the item limit")
	}
	if items+collector.metadataItems > collector.replay.limits.StateItems {
		collector.materializeMetadata()
	}
	collector.replay.sequence.trim(max(1, collector.replay.limits.StateItems-items-collector.metadataItems+1))
	return nil
}

func (collector *ResponseCollector) checkRetention() error {
	if err := collector.reserveItems(0); err != nil {
		return err
	}
	if err := collector.checkRetainedBytes(collector.bytes); err != nil {
		return err
	}
	if collector.retention.budget != nil {
		collector.retention.commit(collector.ownedItems(), collector.bytes)
	}
	return nil
}

func (collector *ResponseCollector) checkRetainedBytes(bytes int) error {
	if bytes+collector.replay.retainedBytes() > collector.limit {
		return streamIssue(LimitExceeded, "/response", "collected response and replay state exceed buffer limit")
	}
	if collector.retention.budget != nil {
		return collector.retention.check(collector.ownedItems(), bytes)
	}
	return nil
}
