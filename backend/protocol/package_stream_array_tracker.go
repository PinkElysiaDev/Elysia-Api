package protocol

func (plan *compiledOutputPlan) trackStreamArray(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	key, err := readPlannedString(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	input, err := plan.body.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(input.output)
	if err != nil {
		return plannedOutput{}, err
	}
	transaction := state.streamTransaction
	base := transaction.primitives()
	channel := streamTrackKey{channel: plan.expression.trackerName, item: key}
	previous, hasPending := transaction.arrayUpdates[channel]
	isNew := false
	if !hasPending {
		var hasPrevious bool
		previous, hasPrevious = base.arrays[channel]
		isNew = !hasPrevious
	}
	next, offset, err := previous.update(state.ctx, items, plan.expression.policy == "snapshot")
	if err != nil {
		return plannedOutput{}, err
	}
	if isNew {
		if err := transaction.reserveTrackChannel(key); err != nil {
			return plannedOutput{}, err
		}
	}
	if transaction.arrayUpdates == nil {
		transaction.arrayUpdates = make(map[streamTrackKey]streamArrayTracker)
	}
	transaction.arrayUpdates[channel] = next
	if offset == 0 {
		return input, nil
	}
	output := JSONArray(items[offset:])
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (transaction *packageStreamTransaction) commitArrays(base *packageStreamPrimitives) {
	if len(transaction.arrayUpdates) != 0 && base.arrays == nil {
		base.arrays = make(map[streamTrackKey]streamArrayTracker)
	}
	for key, tracker := range transaction.arrayUpdates {
		key.item = base.trackedItems[key.item].key
		base.arrays[key] = tracker
	}
}
