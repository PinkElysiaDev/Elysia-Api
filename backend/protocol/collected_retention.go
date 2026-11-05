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
		response.Usage = nil // Replay owns the merged usage until Finish copies it.
		complete, err := measureSemanticValue(&response, collector.replay.limits)
		if err != nil {
			return err
		}
		retained = complete.bytes + collector.unmappedBytes + collector.proofBytes
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
	if isSnapshot {
		additional -= collector.usageItems
	}
	if err := collector.reserveItems(additional); err != nil {
		return err
	}
	if !isSnapshot {
		retained = collector.bytes - collector.headerBytes + headerBytes
	} else {
		collector.usageBytes, collector.usageItems = 0, 0
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
	return collector.replay.retainedItems() + collector.snapshotItems + len(collector.response.Metadata) + len(collector.response.Attributes) + len(collector.unmapped) + collector.usageItems
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
	return collector.checkRetainedState(collector.ownedItems(), bytes)
}

func (collector *ResponseCollector) checkRetainedState(items, bytes int) error {
	if bytes+collector.replay.retainedBytes() > collector.limit {
		return streamIssue(LimitExceeded, "/response", "collected response and replay state exceed buffer limit")
	}
	if collector.retention.budget != nil {
		return collector.retention.check(items, bytes)
	}
	return nil
}

func (collector *ResponseCollector) collectUsage() error {
	items := 0
	if collector.replay.usage != nil {
		items = len(collector.replay.usage.Details)
	}
	additional := items - collector.usageItems
	if err := collector.reserveItems(additional); err != nil {
		return err
	}
	bytes := collector.bytes - collector.usageBytes + collector.replay.usageBytes
	if err := collector.checkRetainedState(collector.ownedItems()+additional, bytes); err != nil {
		return err
	}
	collector.response.Usage = collector.replay.Usage()
	collector.usageBytes, collector.usageItems = collector.replay.usageBytes, items
	collector.bytes = bytes
	collector.retention.commit(collector.ownedItems(), bytes)
	return nil
}
