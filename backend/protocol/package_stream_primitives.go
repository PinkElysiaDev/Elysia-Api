package protocol

import (
	"fmt"
	"strings"
)

const streamTrackersPerItem = 8

func (transaction *packageStreamTransaction) reserveTrackChannel(item string, limits Limits) error {
	base := transaction.primitives()
	channels := base.trackedItems[item] + transaction.trackChannels[item]
	if channels == 0 && len(base.trackedItems)+transaction.trackItems >= limits.StateItems {
		return resourceFailure("stream", "tracked_items", limits.StateItems, len(base.trackedItems)+transaction.trackItems+1)
	}
	if channels == 0 {
		if err := transaction.checkRetainedBytes(len(item)); err != nil {
			return err
		}
		transaction.trackedBytes += len(item)
	}
	if transaction.trackChannels == nil {
		transaction.trackChannels = make(map[string]int)
	}
	transaction.trackChannels[item]++
	if channels == 0 {
		transaction.trackItems++
	}
	return nil
}

type packageStreamPrimitives struct {
	trackedItems  map[string]int
	buffers       map[streamTrackKey]*streamBuffer
	buffered      int
	tools         map[streamTrackKey]*streamTrackedTool
	toolCalls     map[streamTrackKey]string
	toolBuffered  int
	texts         map[streamTrackKey]*TextTracker
	identities    *ItemIdentities
	aliasBytes    int
	trackedBytes  int
	toolCallBytes int
}

func (transaction *packageStreamTransaction) primitives() *packageStreamPrimitives {
	if transaction.primitiveBase == nil {
		if transaction.previous != nil {
			transaction.primitiveBase = transaction.previous.primitives
		}
		if transaction.primitiveBase == nil {
			transaction.primitiveBase = &packageStreamPrimitives{identities: NewItemIdentities(transaction.key.compiled.limits.StateItems)}
		}
	}
	return transaction.primitiveBase
}

func (plan *compiledOutputPlan) resolveStreamItem(state evaluation) (plannedOutput, error) {
	input, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plannedJSONType(input.output) != "object" {
		return plannedOutput{}, fmt.Errorf("resolve_item requires an identity object")
	}
	var event Event
	for _, field := range []struct {
		name   string
		target *Value
	}{{"itemId", &event.ItemID}, {"callId", &event.CallID}, {"parentId", &event.ParentID}} {
		value, err := readOutputPointer(input.output, []string{field.name}, state.valueReader)
		if err != nil {
			return plannedOutput{}, err
		}
		if !value.IsZero() && value.node != nil {
			return plannedOutput{}, fmt.Errorf("item identity requires a scalar value")
		}
		*field.target = value.value
	}
	position, err := readOutputPointer(input.output, []string{"index"}, state.valueReader)
	if err != nil {
		return plannedOutput{}, err
	}
	if !position.IsZero() {
		value, err := readPlannedInteger(position)
		if err != nil || value < 0 {
			return plannedOutput{}, streamIssue(InvalidAssociation, "/index", "item index must be a nonnegative integer")
		}
		index := int(value)
		event.Index = &index
	}
	transaction := state.streamTransaction
	base := transaction.primitives()
	key, aliases, err := resolveItemIdentity(base.identities.aliases, transaction.aliasUpdates, event, base.identities.limit)
	if err != nil {
		return plannedOutput{}, err
	}
	bytes := transaction.aliasBytes
	for _, alias := range aliases.values[:aliases.count] {
		bytes += len(alias)
	}
	if err := transaction.checkRetainedBytes(bytes - transaction.aliasBytes); err != nil {
		return plannedOutput{}, err
	}
	if aliases.count != 0 && transaction.aliasUpdates == nil {
		transaction.aliasUpdates = make(map[string]string, aliases.count)
	}
	for _, alias := range aliases.values[:aliases.count] {
		transaction.aliasUpdates[alias] = key
	}
	transaction.aliasBytes = bytes
	fields := map[string]JSONOutput{"key": JSONLeaf(StringValue(key))}
	if !event.ParentID.IsZero() {
		parent, err := readString(event.ParentID)
		resolved := readItemAlias(base.identities.aliases, transaction.aliasUpdates, "item:"+parent)
		if err != nil || parent == "" || resolved == "" {
			return plannedOutput{}, streamIssue(InvalidAssociation, "/parentId", "parent must reference an already started item")
		}
		fields["parent"] = JSONLeaf(StringValue(resolved))
	}
	output := JSONObject(fields)
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (transaction *packageStreamTransaction) commitPrimitives() *packageStreamPrimitives {
	base := transaction.primitiveBase
	if base == nil {
		if transaction.previous != nil {
			return transaction.previous.primitives
		}
		return nil
	}
	for alias, key := range transaction.aliasUpdates {
		base.identities.aliases[alias] = key
	}
	base.aliasBytes += transaction.aliasBytes
	base.trackedBytes += transaction.trackedBytes
	base.toolCallBytes += transaction.toolCallBytes
	if len(transaction.trackChannels) != 0 && base.trackedItems == nil {
		base.trackedItems = make(map[string]int)
	}
	for item, count := range transaction.trackChannels {
		if count == 0 {
			continue
		}
		if remaining := base.trackedItems[item] + count; remaining != 0 {
			base.trackedItems[strings.Clone(item)] = remaining
		} else {
			delete(base.trackedItems, item)
		}
	}

	transaction.commitBuffers(base)
	transaction.commitTools(base)
	if len(transaction.textUpdates) != 0 && base.texts == nil {
		base.texts = make(map[streamTrackKey]*TextTracker)
	}
	for key, pending := range transaction.textUpdates {
		if base.texts[key] == nil {
			key.item = strings.Clone(key.item)
		}
		for _, delta := range pending.deltas {
			pending.tracker.Append(delta)
		}
		base.texts[key] = pending.tracker
	}
	return base
}
