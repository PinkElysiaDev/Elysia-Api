package protocol

import (
	"context"
	"strconv"
	"strings"
)

type wireEntityState struct {
	definition                 int
	key                        string
	identities                 []string
	identityPaths              []string
	parent                     *wireEntityState
	ordinal                    int64
	hasOrdinal                 bool
	children                   map[int][]*wireEntityState
	properties                 Value
	text                       TextTracker
	array                      []Value
	arrayBytes, arrayNodes     int
	hasArrayValue, arrayIsNull bool
	json                       strings.Builder
	payloadPath                string
	isFinished, sealed         bool
	verifiedFrame              uint64
}

func (state *packageStreamValidation) entityKey(namespace int, field streamField, read func(streamField) (Value, error)) (string, error) {
	if state.contract.namespaces[namespace].keyType == StringType {
		return state.readIdentity(field, read)
	}
	value, err := read(field)
	if err != nil {
		return "", err
	}
	count, err := state.readCount(value, field.path)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(count, 10), nil
}
func (state *packageStreamValidation) consumeEntity(ctx context.Context, action compiledPackageStreamEvent, read func(streamField) (Value, error), reader *valueReader) error {
	key, err := state.entityKey(action.namespace, action.key, read)
	if err != nil {
		return err
	}
	item := state.entities[action.namespace][key]
	isNew := item == nil
	if action.action == wireEntityStart {
		if !isNew && (!action.isImplicit || item.isFinished || item.definition != action.entity) {
			return state.itemIssue(action, action.key.path, "entity start conflicts with its registered key", nil)
		}
		if isNew {
			if state.entityCount >= state.limits.StateItems {
				return state.itemIssue(action, action.key.path, "entity count exceeds the item limit", resourceFailure("stream", "state_items", state.limits.StateItems, state.entityCount+1))
			}
			if err := state.checkRetainedBytes(state.identityBytes+len(key), action.key.path); err != nil {
				return err
			}
			item = &wireEntityState{definition: action.entity, key: strings.Clone(key)}
			definition := state.contract.entities[action.entity]
			item.identities = make([]string, len(definition.identities))
			item.identityPaths = make([]string, len(definition.identities))
		}
	} else if isNew {
		return state.itemIssue(action, action.key.path, "entity action requires a preceding start", nil)
	}
	if action.entity >= 0 && item.definition != action.entity {
		return state.itemIssue(action, action.key.path, "entity action does not match its registered type", nil)
	}
	if action.action == wireEntityVerify && !item.isFinished {
		return state.itemIssue(action, action.key.path, "verification requires a completed entity", nil)
	}
	canVerify := action.action == wireEntityVerify || action.action == wireEntityFinish && action.verifiesCompleted
	if item.isFinished && !canVerify {
		return state.itemIssue(action, action.key.path, "entity received an action after completion", nil)
	}
	if err := state.bindEntityIdentities(item, action, isNew, read); err != nil {
		return err
	}
	if err := state.updateEntityRelations(item, action, isNew, read); err != nil {
		return err
	}
	if err := state.updateEntityProperties(item, action, read); err != nil {
		return err
	}
	if action.delta.path != "" {
		if err := state.consumeEntityPayload(ctx, item, action, read, reader); err != nil {
			return err
		}
	}
	if action.childrenCount.path != "" {
		if err := state.checkEntityChildren(item, action, read); err != nil {
			return err
		}
	}
	if err := state.verifyEntityChildren(item, action); err != nil {
		return err
	}
	if isNew {
		if state.entities[action.namespace] == nil {
			state.entities[action.namespace] = map[string]*wireEntityState{}
		}
		state.entities[action.namespace][item.key] = item
		state.entityOrder = append(state.entityOrder, item)
		state.entityCount++
		state.openItems++
		state.identityBytes += len(item.key)
		if item.parent != nil {
			if item.parent.children == nil {
				item.parent.children = map[int][]*wireEntityState{}
			}
			item.parent.children[action.namespace] = append(item.parent.children[action.namespace], item)
		}
		if state.contract.namespaces[action.namespace].keyType == IntegerType {
			index, _ := strconv.ParseInt(key, 10, 64)
			state.accepted.maxItemIndex = max(state.accepted.maxItemIndex, index)
			state.accepted.hasItemIndex = true
		}
	}
	if action.action == wireEntityFinish && !item.isFinished {
		if err := state.finishEntity(ctx, item, action); err != nil {
			return err
		}
	}
	if action.action == wireEntityFinish || action.action == wireEntityVerify {
		item.verifiedFrame = state.frameNumber
	}
	if action.sealsPayload {
		item.sealed = true
	}
	return nil
}

func (state *packageStreamValidation) bindEntityIdentities(item *wireEntityState, action compiledPackageStreamEvent, isNew bool, read func(streamField) (Value, error)) error {
	definition := state.contract.entities[item.definition]
	for _, selection := range action.identities {
		identity := definition.identities[selection.slot]
		if item.identityPaths[selection.slot] == "" {
			if err := state.checkRetainedBytes(state.identityBytes+len(selection.field.path), selection.field.path); err != nil {
				return err
			}
			item.identityPaths[selection.slot] = strings.Clone(selection.field.path)
			state.identityBytes += len(selection.field.path)
		}
		value, err := read(selection.field)
		if err != nil {
			return err
		}
		if value.IsZero() {
			continue
		}
		if identity.allowsEmpty && value.IsString("") {
			continue
		}
		normalized, err := state.normalizeIdentity(compiledStreamIdentity{kind: identity.kind, isRaw: identity.isRaw, field: selection.field}, value)
		if err != nil {
			return err
		}
		previous := item.identities[selection.slot]
		if previous != "" {
			if previous != normalized {
				return state.itemIssue(action, selection.field.path, "entity identity changed", nil)
			}
			continue
		}
		if item.isFinished {
			return state.itemIssue(action, selection.field.path, "completed entity acquired a new identity", nil)
		}
		if err := state.checkRetainedBytes(state.identityBytes+len(normalized), selection.field.path); err != nil {
			return err
		}
		owned := strings.Clone(normalized)
		if identity.isUnique {
			tableKey := strconv.Itoa(definition.namespace) + ":" + identity.name
			if state.entityIdentityKeys == nil {
				state.entityIdentityKeys = map[string]map[string]*wireEntityState{}
			}
			table := state.entityIdentityKeys[tableKey]
			if owner := table[normalized]; owner != nil && owner != item {
				return state.itemIssue(action, selection.field.path, "entity identity was reused at another key", nil)
			}
			if table == nil {
				table = map[string]*wireEntityState{}
				state.entityIdentityKeys[tableKey] = table
			}
			table[owned] = item
		}
		item.identities[selection.slot] = owned
		state.identityBytes += len(normalized)
	}
	if isNew {
		for slot, identity := range definition.identities {
			if identity.requiredAtStart && item.identities[slot] == "" {
				return state.itemIssue(action, entityIdentityPath(action, slot), "entity start requires its declared identity", nil)
			}
		}
	}
	return nil
}
func entityIdentityPath(action compiledPackageStreamEvent, slot int) string {
	for _, selection := range action.identities {
		if selection.slot == slot {
			return selection.field.path
		}
	}
	return action.key.path
}

func (state *packageStreamValidation) consumeEntityPayload(ctx context.Context, item *wireEntityState, action compiledPackageStreamEvent, read func(streamField) (Value, error), reader *valueReader) error {
	definition := state.contract.entities[item.definition]
	value, err := read(action.delta)
	if err != nil {
		return err
	}
	if definition.isArray {
		return state.consumeEntityArray(ctx, item, action, value, read, reader)
	}
	var text string
	if definition.payload == "json" && action.isJSONValue {
		if value.IsZero() {
			return state.itemIssue(action, action.delta.path, "JSON snapshot is missing", nil)
		}
		text = value.raw
	} else {
		text, err = readString(value)
		if err != nil {
			return state.itemIssue(action, action.delta.path, "entity payload requires a string", err)
		}
	}
	if item.sealed && (action.action == wireEntityAppend || action.sealsPayload) {
		return state.itemIssue(action, action.delta.path, "payload arrived after it was sealed", nil)
	}
	if definition.payload == "text" {
		if action.action == wireEntityAppend {
			item.text.Append(text)
		} else {
			suffix, err := item.text.snapshotSuffix(text)
			if err != nil || (item.isFinished || item.sealed) && suffix != "" {
				return state.itemIssue(action, action.delta.path, "entity snapshot changed its established content", err)
			}
			item.text.Append(suffix)
		}
	} else {
		suffix := text
		if action.action != wireEntityAppend {
			parsed, err := ParseValue([]byte(text))
			if err != nil {
				return state.itemIssue(action, action.delta.path, "entity snapshot contains invalid JSON", err)
			}
			if err := definition.schema.validate(ctx, parsed, action.delta.path, state.limits); err != nil {
				return state.itemIssue(action, action.delta.path, "entity snapshot violates its payload schema", err)
			}
			suffix, err = JSONToolSnapshotSuffix(item.json.String(), text)
			if err != nil {
				return state.itemIssue(action, action.delta.path, "JSON snapshot changed established content", err)
			}
		}
		if (item.sealed || item.isFinished) && suffix != "" {
			return state.itemIssue(action, action.delta.path, "JSON payload changed after it was sealed", nil)
		}
		if err := state.checkRetainedState(state.identityBytes, state.snapshotBytes+len(suffix), action.delta.path); err != nil {
			return err
		}
		item.json.WriteString(suffix)
		state.bufferedBytes += len(suffix)
	}
	return state.retainPayloadLocation(item, action)
}

func (state *packageStreamValidation) retainPayloadLocation(item *wireEntityState, action compiledPackageStreamEvent) error {
	pathBytes := len(action.delta.path) - len(item.payloadPath)
	if err := state.checkRetainedBytes(state.identityBytes+pathBytes, action.delta.path); err != nil {
		return err
	}
	item.payloadPath = strings.Clone(action.delta.path)
	state.identityBytes += pathBytes
	return nil
}
func (state *packageStreamValidation) finishEntity(ctx context.Context, item *wireEntityState, action compiledPackageStreamEvent) error {
	definition := state.contract.entities[item.definition]
	for slot := range definition.identities {
		if item.identities[slot] == "" {
			path := item.identityPaths[slot]
			if path == "" {
				path = entityIdentityPath(action, slot)
			}
			return state.itemIssue(action, path, "entity completion requires its declared identity", nil)
		}
	}
	if definition.isArray {
		if !item.hasArrayValue {
			return state.itemIssue(action, item.payloadLocation(action), "completed array requires an observed value", nil)
		}
		if len(definition.schema.enum) > 0 {
			value := Value{raw: "null"}
			if !item.arrayIsNull {
				var err error
				value, err = EncodeValue(item.array)
				if err != nil {
					return err
				}
			}
			if err := definition.schema.validate(ctx, value, item.payloadLocation(action), state.limits); err != nil {
				return state.itemIssue(action, item.payloadLocation(action), "completed array violates its payload schema", err)
			}
		}
		if !definition.retainCompletedJSON {
			state.bufferedBytes -= item.arrayBytes
			item.array = nil
			item.arrayBytes, item.arrayNodes = 0, 0
		}
	} else if definition.payload == "json" {
		value, err := ParseValue([]byte(item.json.String()))
		if err != nil {
			return state.itemIssue(action, item.payloadLocation(action), "completed entity requires valid JSON", err)
		}
		if err := definition.schema.validate(ctx, value, item.payloadLocation(action), state.limits); err != nil {
			return state.itemIssue(action, item.payloadLocation(action), "completed entity violates its payload schema", err)
		}
		if !definition.retainCompletedJSON {
			state.bufferedBytes -= item.json.Len()
			item.json.Reset()
		}
	}
	for _, namespace := range definition.childNamespaces {
		for _, child := range item.children[namespace] {
			if child.isFinished {
				continue
			}
			if !state.contract.entities[child.definition].closeWithParent {
				return state.itemIssue(action, action.key.path, "container finished before its children", nil)
			}
			if err := state.finishEntity(ctx, child, action); err != nil {
				return err
			}
		}
	}

	item.isFinished = true
	state.openItems--
	return nil
}
func (item *wireEntityState) payloadLocation(action compiledPackageStreamEvent) string {
	if item.payloadPath != "" {
		return item.payloadPath
	}
	return action.key.path
}
func (state *packageStreamValidation) finishEntities(ctx context.Context, action compiledPackageStreamEvent) error {
	for _, item := range state.entityOrder {
		for _, required := range action.requiredEntities {
			if item.definition == required && item.verifiedFrame != state.frameNumber {
				return state.itemIssue(action, state.contract.discriminator.path, "terminal snapshot omitted a registered entity", nil)
			}
		}
	}
	if state.openItems != 0 && !state.contract.finishOpenEntities {
		return state.itemIssue(action, state.contract.discriminator.path, "stream finished before its entities", nil)
	}
	// Parents are registered before children. Complete children first without sorting.
	for index := len(state.entityOrder) - 1; index >= 0; index-- {
		item := state.entityOrder[index]
		if !item.isFinished {
			if err := state.finishEntity(ctx, item, action); err != nil {
				return err
			}
		}
	}
	return nil
}
