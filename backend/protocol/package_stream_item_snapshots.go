package protocol

import "slices"

func (state *packageStreamValidation) itemIssue(action compiledPackageStreamEvent, path, reason string, cause error) error {
	return state.boundaryIssue(action.diagnostic, path, reason, cause)
}
func (state *packageStreamValidation) updateEntityRelations(item *wireEntityState, action compiledPackageStreamEvent, isNew bool, read func(streamField) (Value, error)) error {
	if action.parent.path == "" {
		return nil
	}
	definition := state.contract.entities[item.definition]
	parentKey, err := state.entityKey(definition.parentNamespace, action.parent, read)
	if err != nil {
		return err
	}
	parent := state.entities[definition.parentNamespace][parentKey]
	if parent == nil {
		return state.itemIssue(action, action.parent.path, "entity requires a registered parent", nil)
	}
	if len(definition.parentEntities) > 0 && !slices.Contains(definition.parentEntities, parent.definition) {
		return state.itemIssue(action, action.parent.path, "parent entity does not satisfy its declared relation", nil)
	}
	if isNew {
		if parent.isFinished {
			return state.itemIssue(action, action.parent.path, "entity cannot start beneath a completed parent", nil)
		}
		item.parent = parent
	} else if item.parent != parent {
		return state.itemIssue(action, action.parent.path, "entity parent changed", nil)
	}
	if action.ordinal.path == "" {
		return nil
	}
	value, err := read(action.ordinal)
	if err != nil {
		return err
	}
	ordinal, err := state.readCount(value, action.ordinal.path)
	if err != nil {
		return err
	}
	children := parent.children[definition.namespace]
	if isNew {
		if ordinal != int64(len(children)) {
			return state.itemIssue(action, action.ordinal.path, "child must append at its namespace ordinal", nil)
		}
		item.ordinal, item.hasOrdinal = ordinal, true
	} else if !item.hasOrdinal || item.ordinal != ordinal || ordinal >= int64(len(children)) || children[ordinal] != item {
		return state.itemIssue(action, action.ordinal.path, "child snapshot changed its position", nil)
	}
	return nil
}
func (state *packageStreamValidation) checkEntityChildren(item *wireEntityState, action compiledPackageStreamEvent, read func(streamField) (Value, error)) error {
	value, err := read(action.childrenCount)
	if err != nil || value.IsZero() {
		return err
	}
	count, err := state.readCount(value, action.childrenCount.path)
	if err != nil {
		return err
	}
	children := item.children[action.childrenNamespace]
	if count != int64(len(children)) {
		return state.itemIssue(action, action.childrenCount.path, "container snapshot does not match its registered children", nil)
	}
	if action.action != wireEntityStart {
		for _, child := range children {
			if child.verifiedFrame != state.frameNumber {
				return state.itemIssue(action, action.childrenCount.path, "container snapshot omitted a completed child", nil)
			}
		}
	}
	return nil
}
func (state *packageStreamValidation) updateEntityProperties(item *wireEntityState, action compiledPackageStreamEvent, read func(streamField) (Value, error)) error {
	if action.properties.path == "" {
		return nil
	}
	value, err := read(action.properties)
	if err != nil {
		return err
	}
	if value.IsZero() {
		return nil
	}
	properties, err := mergeStreamProperties(item.properties, value, action.action == wireEntityVerify)
	if err != nil {
		return state.itemIssue(action, action.properties.path, "entity properties require an object", err)
	}
	if item.isFinished && !equalValues(properties, item.properties) {
		return state.itemIssue(action, action.properties.path, "completed entity properties changed", nil)
	}
	bytes := len(properties.raw) - len(item.properties.raw)
	if err := state.checkRetainedState(state.identityBytes, state.snapshotBytes+bytes, action.properties.path); err != nil {
		return err
	}
	item.properties = properties
	state.snapshotBytes += bytes
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
