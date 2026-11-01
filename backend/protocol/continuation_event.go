package protocol

import "slices"

// An entity shares the collector's identity and owns only proof metadata. Its
// completed digest authenticates the full content, never a snapshot prefix.
type continuationEntity struct {
	binding      *ContinuationBinding
	key, digest  string
	isComplete   bool
	isChanged    bool
	isRecovering bool
}

type continuationEventObservation struct {
	original Event
	entity   *continuationEntity
}

func (entity *continuationEntity) complete(node Node) {
	entity.isComplete = true
	if HasContinuationState(node) {
		entity.digest = ContinuationNodeDigest(node)
	}
}

// BindContinuationEvent associates an already consumed source event with the
// collector's entity. Call it after capturing any newly completed nodes.
func (collector *ResponseCollector) BindContinuationEvent(event Event, binding *ContinuationBinding) Event {
	if binding == nil {
		return event
	}
	if event.Type == ResponseFinished {
		for _, position := range collector.items {
			if entity := position.continuation; entity != nil && !entity.isComplete {
				entity.complete(*collector.item(entity.key))
			}
		}
		return event
	}
	switch event.Type {
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
	default:
		return event
	}
	key, err := collector.replay.itemKey(event)
	if err != nil {
		panic("consumed continuation event lost its item identity")
	}
	position := collector.items[key]
	if position.collection != "" {
		key = position.parent
		position = collector.items[key]
	}
	if collector.replay.items[key].kind == MessageNode {
		return event
	}
	if position.continuation == nil {
		position.continuation = &continuationEntity{binding: binding, key: key}
		collector.items[key] = position
	}
	event.continuation = position.continuation
	return event
}

func sameContinuationEvent(left, right Event) bool {
	if left.Index == nil || right.Index == nil {
		if left.Index != right.Index {
			return false
		}
	} else if *left.Index != *right.Index {
		return false
	}
	// Response identity is a target presentation decision; item identity and
	// parent/index associations still bind this event to its collected content.
	return left.Type == right.Type && left.Collection == right.Collection &&
		equalValues(left.ItemID, right.ItemID) && equalValues(left.ParentID, right.ParentID) &&
		equalValues(left.CallID, right.CallID) && equalValues(left.Delta, right.Delta) &&
		sameContinuationNode(left.Item, right.Item)
}

// Presentation IDs, provenance and removed protected resources are excluded by
// ContinuationNodeDigest. Compare remaining fields without encoding prefixes.
func sameContinuationNode(left, right *Node) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	a, b := CanonicalReasoning(*left), CanonicalReasoning(*right)
	if a.Kind != b.Kind || a.ReasoningForm != b.ReasoningForm ||
		!equalValues(a.Payload, b.Payload) || !equalValues(a.CallID, b.CallID) || !equalValues(a.Name, b.Name) {
		return false
	}
	if a.Input == nil || b.Input == nil {
		if a.Input != b.Input {
			return false
		}
	} else if a.Input.Kind != b.Input.Kind || !equalValues(a.Input.Value, b.Input.Value) {
		return false
	}
	if !slices.EqualFunc(a.Metadata, b.Metadata, func(x, y ResponseMetadata) bool {
		value := equalValues(x.Value, y.Value)
		x.Value, y.Value = Value{}, Value{}
		return value && x == y
	}) {
		return false
	}
	equalNode := func(x, y Node) bool { return sameContinuationNode(&x, &y) }
	return slices.EqualFunc(a.Children, b.Children, equalNode) &&
		(a.ReasoningContent == nil) == (b.ReasoningContent == nil) && slices.EqualFunc(a.ReasoningContent, b.ReasoningContent, equalNode)
}
