package protocol

import (
	"reflect"
	"strings"
)

// Server execution snapshots replace their structured results. Only declared
// text input uses the existing incremental buffer; none is assistant output.
func (collector *ResponseCollector) collectServerTool(key string, event Event) (NodeKind, string, error) {
	if event.Type == ItemDelta && event.Item != nil {
		return collector.updateServerToolHeader(key, event.Item)
	}
	buffer := collector.texts[key]
	previousBytes := 0
	if buffer != nil {
		previousBytes = buffer.Len()
	}
	isSnapshot, isFinished := event.Type != ItemDelta, event.Type == ItemFinished
	var snapshot Node
	if event.Item != nil {
		snapshot = *event.Item
	} else {
		snapshot = *collector.item(key)
	}
	value := event.Delta
	if isSnapshot {
		value = Value{}
		if snapshot.Input != nil && snapshot.Input.Kind == TextInput {
			value = snapshot.Input.Value
		}
	}
	hasInput := !value.IsZero() || buffer != nil
	var delta string
	if !value.IsZero() {
		text, err := readString(value)
		if err != nil {
			return "", "", err
		}
		delta = text
		if isSnapshot {
			delta = text[previousBytes:] // Replay verified this snapshot's prefix.
		}
	}
	nextBytes := previousBytes + len(delta)
	if snapshot.Input != nil && snapshot.Input.Kind == TextInput {
		input := *snapshot.Input
		snapshot.Input = &input
		if isFinished && event.Item == nil && buffer != nil {
			input.Value = StringValue(buffer.String())
		} else if !isFinished && hasInput {
			input.Value = Value{}
		}
	}
	if isFinished {
		nextBytes = 0
	}
	metadataBytes := collector.metadata[key]
	if isSnapshot {
		size, err := measureSemanticValue(&snapshot, collector.replay.limits)
		if err != nil {
			return "", "", err
		}
		metadataBytes = size.bytes
	}
	retained := collector.bytes - collector.metadata[key] - previousBytes + metadataBytes + nextBytes
	if retained > collector.limit {
		return "", "", streamIssue(LimitExceeded, "/item", "server execution snapshot exceeds the response buffer limit")
	}
	if isSnapshot {
		owned := cloneServerTool(&snapshot)
		if event.Type == ItemStarted {
			collector.registerItem(key, event, owned)
		} else {
			*collector.item(key) = owned
		}
		collector.metadata[key] = metadataBytes
	}
	if isFinished {
		delete(collector.texts, key)
	} else if hasInput {
		if buffer == nil {
			buffer = &collectedText{}
			collector.texts[key] = buffer
		}
		buffer.WriteString(delta)
		buffer.dirty = true
	}
	collector.bytes, collector.hasServerTools = retained, true
	return ServerToolNode, "", nil
}

func (collector *ResponseCollector) updateServerToolHeader(key string, header *Node) (NodeKind, string, error) {
	item := collector.item(key)
	before := Node{Kind: item.Kind, ID: item.ID, Name: item.Name, Status: item.Status, Source: item.Source}
	after := before
	if !header.ID.IsZero() && item.ID.IsZero() {
		after.ID = Value{raw: strings.Clone(header.ID.raw)}
	}
	if after.Status != header.Status {
		after.Status = Value{raw: strings.Clone(header.Status.raw)}
	}
	if header.Source != nil {
		source := *header.Source
		after.Source = &source
	}
	// Progress owns only a small header; accumulated input and immutable media
	// remain with the existing item and are neither rescanned nor copied.
	previous, err := measureSemanticValue(&before, collector.replay.limits)
	if err != nil {
		return "", "", err
	}
	next, err := measureSemanticValue(&after, collector.replay.limits)
	if err != nil {
		return "", "", err
	}
	difference := next.bytes - previous.bytes
	if collector.bytes+difference > collector.limit {
		return "", "", streamIssue(LimitExceeded, "/item", "server progress exceeds the response buffer limit")
	}
	item.ID, item.Status, item.Source = after.ID, after.Status, after.Source
	collector.bytes += difference
	collector.metadata[key] += difference
	return ServerToolNode, "", nil
}

func cloneServerTool(node *Node) Node {
	// Native, source, maps and slices own their mutable headers. Immutable
	// Value bytes remain shared, including media retained by the live snapshot.
	return *cloneSemantic(reflect.ValueOf(node), typedLimitPlans[reflect.TypeFor[*Node]()]).Interface().(*Node)
}

func (collector *ResponseCollector) ownServerResponseContent() {
	if collector.hasServerTools || len(collector.items) == 0 && hasServerContent(collector.response.Content) {
		collector.response.Content, _ = ownServerContent(collector.response.Content)
		collector.hasServerTools = true
	}
}

func hasServerContent(nodes []Node) bool {
	for _, node := range nodes {
		if node.Kind == ServerToolNode || hasServerContent(node.Children) {
			return true
		}
	}
	return false
}

// Copy only paths containing server snapshots; existing content retains its
// established collection boundary, while new mutable artifact containers own
// their headers without copying immutable media bytes.
func ownServerContent(nodes []Node) ([]Node, bool) {
	var result []Node
	for index, node := range nodes {
		if node.Kind == ServerToolNode {
			node = cloneServerTool(&node)
		} else if children, changed := ownServerContent(node.Children); changed {
			node.Children = children
		} else {
			continue
		}
		if result == nil {
			result = append([]Node(nil), nodes...)
		}
		result[index] = node
	}
	if result == nil {
		return nodes, false
	}
	return result, true
}
