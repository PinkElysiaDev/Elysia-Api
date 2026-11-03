package protocol

import (
	"context"
	"slices"
)

// The projection excludes authority, resources and arbitrary nested content.
// An origin retains immutable values and its own metadata containers only until
// the current event is encoded. Long-lived state retains digests and intervals.
var semanticRangeEventSchema = ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{
	"type": {Type: StringType}, "responseId": {Type: AnyType}, "itemId": {Type: AnyType},
	"parentId": {Type: AnyType}, "collection": {Type: StringType}, "delta": {Type: AnyType},
	"metadata": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}},
	"item": {Type: ObjectType, Properties: map[string]ValueSchema{
		"kind": {Type: StringType}, "payload": {Type: AnyType},
		"metadata": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}},
	}},
}}

func captureEventRangeOrigin(event Event) *Event {
	if event.Item == nil && event.Delta.IsZero() && len(event.Metadata) == 0 {
		return nil
	}
	origin := &Event{Type: event.Type, ResponseID: event.ResponseID, ItemID: event.ItemID,
		ParentID: event.ParentID, Collection: event.Collection, Delta: event.Delta, Metadata: slices.Clone(event.Metadata)}
	if event.Item != nil {
		origin.Item = &Node{Kind: event.Item.Kind, Payload: event.Item.Payload, Metadata: slices.Clone(event.Item.Metadata)}
	}
	return origin
}

func sameEventRangeInput(before, after *Event) bool {
	if before.Type != after.Type || before.ResponseID != after.ResponseID || before.ItemID != after.ItemID ||
		before.ParentID != after.ParentID || before.Collection != after.Collection || before.Delta != after.Delta ||
		!slices.Equal(before.Metadata, after.Metadata) {
		return false
	}
	if before.Item == nil || after.Item == nil {
		return before.Item == after.Item
	}
	return before.Item.Kind == after.Item.Kind && before.Item.Payload == after.Item.Payload && slices.Equal(before.Item.Metadata, after.Item.Metadata)
}

func semanticRangeEvaluation(ctx context.Context, event *Event, limits Limits) evaluation {
	read := func(pointer []string) (JSONOutput, error) { return readSemanticRangeEvent(event, pointer) }
	return evaluation{ctx: ctx, inputReader: read, rootReader: read, valueReader: &valueReader{},
		budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}}
}

// Whole-object reads expose the same projection as statically bound paths.
// Direct field reads borrow immutable leaves without copying the event body.
func readSemanticRangeEvent(event *Event, pointer []string) (JSONOutput, error) {
	if len(pointer) != 0 {
		if pointer[0] == "item" && len(pointer) == 1 && event.Item != nil {
			item := Node{Kind: event.Item.Kind, Payload: event.Item.Payload, Metadata: event.Item.Metadata}
			return readCoreNode(item, nil)
		}
		return readCoreEventOutput(event, pointer)
	}
	fields := make([]jsonOutputField, 0, len(semanticRangeEventSchema.Properties))
	for _, name := range []string{"type", "responseId", "itemId", "parentId", "collection", "delta", "metadata", "item"} {
		value, err := readSemanticRangeEvent(event, []string{name})
		if err != nil {
			return JSONOutput{}, err
		}
		if !value.IsZero() {
			fields = append(fields, jsonOutputField{name, value})
		}
	}
	return JSONOutput{node: &jsonOutputNode{object: true, fields: fields}}, nil
}

func (compiled *Compiled) checkSemanticEventRanges(ctx context.Context, direction Direction, event *Event, options EvaluationContext) error {
	declarations := compiled.mappings[direction].semanticRanges
	if len(declarations) == 0 {
		return nil
	}
	before := event.rangeOrigin
	if before == nil {
		before = event // Authored core input is its own baseline, like wire preview.
	}
	transaction := options.packageTransaction
	if transaction.semanticRanges == nil && transaction.previous != nil && transaction.previous.semanticRanges != nil {
		copy := *transaction.previous.semanticRanges
		copy.pending = nil
		transaction.semanticRanges = &copy
	}
	limits := compiled.limits
	if err := consumeRangeDeclarations(&transaction.semanticRanges, declarations,
		semanticRangeEvaluation(ctx, before, limits), semanticRangeEvaluation(ctx, event, limits),
		sameEventRangeInput(before, event), limits); err != nil {
		return compiled.mappingError(direction, err)
	}
	if transaction.semanticRanges != nil {
		base := transaction.primitives()
		items := len(base.trackedItems) + transaction.trackItems + base.positionItems + transaction.positionItems + transaction.semanticRanges.items
		if items > limits.StateItems {
			return resourceFailure("stream", "state_items", limits.StateItems, items)
		}
	}
	return transaction.checkRetainedBytes(0)
}
