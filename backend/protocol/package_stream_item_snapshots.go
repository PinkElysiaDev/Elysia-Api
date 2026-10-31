package protocol

import (
	"slices"
	"strings"
)

// Container snapshots retain only associations and owned wire properties.
// Text equality reuses EventReplay's digest, so sealing never keeps a prefix.
type streamItemSnapshot struct {
	properties    Value
	status        Value
	children      []string
	parent        string
	verifiedFrame uint64
	sealedText    bool
}

func (state *packageStreamValidation) itemIssue(action compiledPackageStreamEvent, path, reason string, cause error) error {
	return state.boundaryIssue(action.diagnostic, path, reason, cause)
}

func (state *packageStreamValidation) prepareItemSnapshot(event Event, action compiledPackageStreamEvent, read func(streamField) (Value, error)) (*streamItemSnapshot, int, bool, error) {
	if event.ItemID.IsZero() || action.isTool {
		return nil, 0, false, nil
	}
	id, _ := readString(event.ItemID)
	previous := state.itemSnapshots[id]
	if event.Type == ItemDelta && action.ordinal.path == "" {
		if previous != nil && previous.sealedText {
			return nil, 0, false, state.itemIssue(action, action.itemID.path, "text arrived after its completion", nil)
		}
		return nil, 0, false, nil
	}
	usesSnapshot := action.kind == MessageNode || action.parentID.path != "" || action.properties.path != "" || action.sealsText || action.verifiesItem || action.verifiesCompleted
	if previous == nil && !usesSnapshot {
		return nil, 0, false, nil
	}
	snapshot := &streamItemSnapshot{}
	if previous != nil {
		*snapshot = *previous
	}
	item := state.replay.items[state.replay.identities.aliases["item:"+id]]
	if action.verifiesItem && (item == nil || !item.isFinished) {
		return nil, 0, false, state.itemIssue(action, action.itemID.path, "snapshot requires a completed item", nil)
	}
	isCompleted := item != nil && item.isFinished
	skipReplay := action.verifiesItem || action.verifiesCompleted && isCompleted
	if snapshot.sealedText && (event.Type == ItemDelta || action.sealsText) {
		return nil, 0, false, state.itemIssue(action, action.itemID.path, "text arrived after its completion", nil)
	}
	if snapshot.sealedText || skipReplay && action.kind != MessageNode {
		if err := state.checkSealedText(id, event, action); err != nil {
			return nil, 0, false, err
		}
	}
	if skipReplay {
		if err := state.checkCompletedItem(event, action, item, snapshot); err != nil {
			return nil, 0, false, err
		}
	}
	bytes := 0
	if event.Item != nil && event.Item.Kind == MessageNode {
		if item == nil || item.wireID.IsZero() {
			bytes += len(event.Item.ID.raw)
		}
		if item == nil || item.role.IsZero() {
			bytes += len(event.Item.Role.raw)
		}
	}
	associationBytes, err := state.checkItemOrdinal(id, event, action, snapshot, read)
	if err != nil {
		return nil, 0, false, err
	}
	bytes += associationBytes
	if err := state.checkContainerChildren(event, action, snapshot, read); err != nil {
		return nil, 0, false, err
	}
	propertyBytes, err := state.updateItemProperties(action, snapshot, isCompleted, read)
	if err != nil {
		return nil, 0, false, err
	}
	bytes += propertyBytes
	if event.Item != nil && !event.Item.Status.IsZero() {
		bytes += len(event.Item.Status.raw) - len(snapshot.status.raw)
		snapshot.status = event.Item.Status
	}
	snapshot.sealedText = snapshot.sealedText || action.sealsText
	if event.Type == ItemFinished || action.verifiesItem {
		snapshot.verifiedFrame = state.frameNumber
	}
	return snapshot, bytes, skipReplay, nil
}

func (state *packageStreamValidation) checkCompletedItem(event Event, action compiledPackageStreamEvent, item *replayItem, snapshot *streamItemSnapshot) error {
	parent, err := state.replay.identities.Parent(event.ParentID)
	if err != nil {
		return state.itemIssue(action, action.parentID.path, "completed item parent is not registered", err)
	}
	if parent != item.parent {
		return state.itemIssue(action, action.parentID.path, "completed item parent changed", nil)
	}
	if event.Item.Kind != item.kind {
		return state.itemIssue(action, action.itemID.path, "completed item changed kind", nil)
	}
	for _, field := range []struct {
		actual, expected Value
		path             string
	}{
		{event.Item.ID, item.wireID, action.wireID.path}, {event.Item.Role, item.role, action.role.path}, {event.Item.Status, snapshot.status, action.status.path},
	} {
		if !field.actual.IsZero() && field.actual != field.expected {
			return state.itemIssue(action, field.path, "completed item identity, role or status changed", nil)
		}
	}
	return nil
}

func (state *packageStreamValidation) checkItemOrdinal(id string, event Event, action compiledPackageStreamEvent, snapshot *streamItemSnapshot, read func(streamField) (Value, error)) (int, error) {
	if action.ordinal.path == "" {
		return 0, nil
	}
	parent, _ := readString(event.ParentID)
	owner := state.itemSnapshots[parent]
	if owner == nil {
		return 0, state.itemIssue(action, action.parentID.path, "content requires a started container", nil)
	}
	value, err := read(action.ordinal)
	if err != nil {
		return 0, err
	}
	ordinal, err := state.readCount(value, action.ordinal.path)
	if err != nil {
		return 0, err
	}
	if event.Type == ItemStarted {
		if ordinal != int64(len(owner.children)) {
			return 0, state.itemIssue(action, action.ordinal.path, "content must append to its container in order", nil)
		}
		snapshot.parent = strings.Clone(parent)
		return len(parent) + len(id), nil
	}
	if ordinal >= int64(len(owner.children)) || owner.children[ordinal] != id {
		return 0, state.itemIssue(action, action.ordinal.path, "content snapshot changed its container position", nil)
	}
	return 0, nil
}

func (state *packageStreamValidation) checkContainerChildren(event Event, action compiledPackageStreamEvent, snapshot *streamItemSnapshot, read func(streamField) (Value, error)) error {
	if action.childrenCount.path == "" {
		return nil
	}
	value, err := read(action.childrenCount)
	if err != nil || value.IsZero() {
		return err
	}
	count, err := state.readCount(value, action.childrenCount.path)
	if err != nil {
		return err
	}
	if count != int64(len(snapshot.children)) {
		return state.itemIssue(action, action.childrenCount.path, "container snapshot does not match its started children", nil)
	}
	if event.Type == ItemStarted {
		return nil
	}
	for _, child := range snapshot.children {
		if state.itemSnapshots[child].verifiedFrame != state.frameNumber {
			return state.itemIssue(action, action.childrenCount.path, "container snapshot omitted a completed child", nil)
		}
	}
	return nil
}

func (state *packageStreamValidation) updateItemProperties(action compiledPackageStreamEvent, snapshot *streamItemSnapshot, isCompleted bool, read func(streamField) (Value, error)) (int, error) {
	if action.properties.path == "" {
		return 0, nil
	}
	value, err := read(action.properties)
	if err != nil {
		return 0, err
	}
	properties, err := mergeStreamProperties(snapshot.properties, value, action.verifiesItem)
	if err != nil {
		return 0, state.itemIssue(action, action.properties.path, "item properties require an object", err)
	}
	if isCompleted && !equalValues(properties, snapshot.properties) {
		return 0, state.itemIssue(action, action.properties.path, "completed item properties changed", nil)
	}
	bytes := len(properties.raw) - len(snapshot.properties.raw)
	snapshot.properties = properties
	return bytes, nil
}

func (state *packageStreamValidation) checkSealedText(id string, event Event, action compiledPackageStreamEvent) error {
	if event.Item == nil || event.Item.Payload.IsZero() {
		return nil
	}
	text, err := readString(event.Item.Payload)
	if err != nil {
		return state.itemIssue(action, action.delta.path, "completed text requires a string", err)
	}
	key := state.replay.identities.aliases["item:"+id]
	tracker := state.replay.state.texts[key]
	if tracker == nil {
		tracker = &TextTracker{}
	}
	suffix, err := tracker.snapshotSuffix(text)
	if err != nil || suffix != "" {
		return state.itemIssue(action, action.delta.path, "completed item text changed", err)
	}
	return nil
}

func mergeStreamProperties(previous, update Value, replace bool) (Value, error) {
	fields, err := update.ReadObject()
	if err != nil {
		return Value{}, err
	}
	if !replace && !previous.IsZero() {
		current, err := previous.ReadObject()
		if err != nil {
			return Value{}, err
		}
		for key, value := range fields {
			current[key] = value
		}
		fields = current
	}
	return EncodeValue(fields)
}

func (state *packageStreamValidation) commitItemSnapshot(event Event, action compiledPackageStreamEvent, snapshot *streamItemSnapshot) {
	if snapshot == nil {
		return
	}
	id, _ := readString(event.ItemID)
	if state.itemSnapshots == nil {
		state.itemSnapshots = make(map[string]*streamItemSnapshot)
	}
	id = strings.Clone(id)
	state.itemSnapshots[id] = snapshot
	if event.Type == ItemStarted && action.ordinal.path != "" {
		owner := state.itemSnapshots[snapshot.parent]
		owner.children = append(owner.children, id)
	}
}

func (state *packageStreamValidation) checkRequiredItems(action compiledPackageStreamEvent) error {
	if len(action.requiredItems) == 0 {
		return nil
	}
	for id, snapshot := range state.itemSnapshots {
		item := state.replay.items[state.replay.identities.aliases["item:"+id]]
		if item != nil && slices.Contains(action.requiredItems, item.kind) && snapshot.verifiedFrame != state.frameNumber {
			return state.itemIssue(action, action.itemID.path, "terminal snapshot omitted a registered container", nil)
		}
	}
	return nil
}
