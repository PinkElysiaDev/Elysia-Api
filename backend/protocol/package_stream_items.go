package protocol

func (state *packageStreamValidation) readItemPayload(event *Event, definition *compiledPackageStreamEvent, read func(streamField) (Value, error)) (int, error) {
	itemID, err := state.readItemIdentity(definition.itemID, read)
	if err != nil {
		return 0, err
	}
	event.ItemID = StringValue(itemID)
	// Generic finishes close the established item, as on transports whose
	// end marker carries no kind. Tool finishes still promise a tool item.
	if (state.contract.hasTools || state.contract.hasTypedItems) && (event.Type == ItemDelta || (event.Type == ItemSnapshot || event.Type == ItemFinished) && definition.isTool) {
		key := state.replay.identities.aliases["item:"+itemID]
		if item := state.replay.items[key]; item != nil && ((item.kind == ToolCallNode) != definition.isTool || event.Type == ItemDelta && !definition.isTool && item.kind != definition.kind) {
			return 0, state.issue(definition.itemID.path, "stream action does not match the established item kind", nil)
		}
	}
	if event.Type == ItemStarted {
		return state.readStartedItem(event, definition, itemID, read)
	}
	if event.Type == ItemSnapshot {
		value, err := read(definition.delta)
		if err != nil {
			return 0, err
		}
		text, err := readString(value)
		if err != nil {
			return 0, state.issue(definition.delta.path, "tool argument snapshot must be a JSON string", err)
		}
		arguments, err := ParseValue([]byte(text))
		if err != nil {
			return 0, state.issue(definition.delta.path, "tool argument snapshot contains invalid JSON", err)
		}
		event.Item = &Node{Kind: ToolCallNode, Input: &ToolInput{Kind: JSONInput, Value: arguments}}
	}
	if event.Type == ItemDelta {
		event.Delta, err = read(definition.delta)
		if err != nil {
			return 0, err
		}
		if !event.Delta.IsStringValue() {
			if definition.isTool {
				return 0, state.issue(definition.delta.path, "tool arguments delta must be a string", nil)
			}
			return 0, state.issue(definition.delta.path, "text delta must be a string", nil)
		}
	}
	return 0, nil
}

func (state *packageStreamValidation) readStartedItem(event *Event, definition *compiledPackageStreamEvent, itemID string, read func(streamField) (Value, error)) (int, error) {
	previous := state.replay.items[state.replay.identities.aliases["item:"+itemID]]
	itemBytes := len(itemID)
	if definition.isImplicit && previous != nil {
		kind := definition.kind
		if definition.isTool {
			kind = ToolCallNode
		}
		if previous.isFinished || previous.kind != kind {
			return 0, state.issue(definition.itemID.path, "implicit start conflicts with the established item", nil)
		}
		itemBytes = 0
		if definition.isTool {
			event.Type = ItemSnapshot
		}
	}
	if definition.isTool {
		name, nameBytes, err := state.readToolIdentity(definition.tool.name, definition.isSparseIdentity, read)
		if err != nil {
			return 0, err
		}
		callID, callBytes, err := state.readToolIdentity(definition.tool.callID, definition.isSparseIdentity, read)
		if err != nil {
			return 0, err
		}
		if previous != nil {
			if previous.name != "" {
				nameBytes = 0
			}
			if previous.callID != "" {
				callBytes = 0
			}
		}
		event.Item = &Node{Kind: ToolCallNode, Name: name, CallID: callID, Input: &ToolInput{Kind: JSONInput}}
		return itemBytes + nameBytes + callBytes, nil
	}
	event.Item = &Node{Kind: definition.kind}
	return itemBytes, nil
}
