package protocol

type streamToolPaths struct{ name, callID, arguments string }

func (paths streamToolPaths) bytes() int {
	return len(paths.name) + len(paths.callID) + len(paths.arguments)
}

// Implicit terminals can discover errors after the tool's last argument frame.
// Keep locations only, so the final diagnostic does not retain that frame body.
func (state *packageStreamValidation) prepareToolPaths(event Event, action compiledPackageStreamEvent) (string, streamToolPaths, int) {
	if !state.contract.closeItemsOnFinish || !action.isTool {
		return "", streamToolPaths{}, 0
	}
	itemID, _ := readString(event.ItemID)
	key := state.replay.identities.aliases["item:"+itemID]
	if key == "" {
		key = "item:" + itemID
	}
	previous := state.toolPaths[key]
	next := previous
	if action.tool != nil {
		next.name, next.callID = action.tool.name.path, action.tool.callID.path
		if next.arguments == "" {
			next.arguments = action.itemID.path
		}
	}
	if action.action == ItemDelta {
		next.arguments = action.delta.path
	}
	return key, next, next.bytes() - previous.bytes()
}
