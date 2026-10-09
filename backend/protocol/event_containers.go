package protocol

// Explicit message events preserve boundaries and identities. Protocols which
// expose only leaves still use the established wrapper equivalence instead.
func collectedContainers(nodes []Node) []Node {
	out := cloneNodes(nodes)
	for i := range out {
		n := &out[i]
		*n = CanonicalReasoning(*n)
		n.Native, n.Source = nil, nil
		n.Metadata = comparableMetadata(n.Metadata)
		n.Children = collectedContainers(n.Children)
		if n.Kind != MessageNode {
			n.ID, n.Status = Value{}, Value{}
		}
		if n.ReasoningContent != nil {
			n.ReasoningContent = collectedContainers(n.ReasoningContent)
			if n.ReasoningContent == nil {
				n.ReasoningContent = []Node{}
			}
		}
	}
	return out
}

func hasMessageEvents(events []Event) bool {
	for _, event := range events {
		if event.Item != nil && event.Item.Kind == MessageNode {
			return true
		}
	}
	return false
}
