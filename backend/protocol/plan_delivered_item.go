package protocol

import (
	"crypto/sha256"
	"encoding"
	"fmt"
	"strconv"
)

// Delivered items are borrowed only while validation has no newer, unsent
// state. Resuming copies the digest once; native-only streams keep no renderer
// item or second text tracker.
func (runtime evaluationRuntime) deliveredItems() (*packageStreamValidation, error) {
	state := runtime.outputStream
	if state == nil {
		return nil, nil
	}
	if state.hasRejectedFrame || state.frameNumber != state.deliveredFrameNumber {
		return nil, fmt.Errorf("item continuation requires completely delivered output state")
	}
	return state, nil
}

func (runtime evaluationRuntime) deliveredItem(id string) (*replayItem, *TextTracker, error) {
	state, err := runtime.deliveredItems()
	if err != nil || state == nil {
		return nil, nil, err
	}
	key := state.replay.identities.aliases["item:"+id]
	return state.replay.items[key], state.replay.state.texts[key], nil
}

func (plan *compiledOutputPlan) readDeliveredItem(state evaluation) (plannedOutput, error) {
	selected, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	id, err := readPlannedString(selected.output)
	if err != nil {
		return plannedOutput{}, err
	}
	item, _, err := state.runtime.deliveredItem(id)
	if err == nil && state.runtime.outputStream != nil {
		state.budget.hasReadOutputStream = true
	}
	if err != nil || item == nil {
		return plannedOutput{}, err
	}
	fields := map[string]JSONOutput{
		"kind":       JSONLeaf(StringValue(string(item.kind))),
		"isFinished": JSONLeaf(coreBoolean(item.isFinished)),
	}
	if state.runtime.outputStream.contract.isItemIDIndex {
		// The contract stored this key only after readCount and FormatInt.
		index, _ := strconv.ParseInt(id, 10, 64)
		fields["index"] = JSONLeaf(coreInteger(index))
	}
	output := JSONObject(fields)
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (plan *compiledOutputPlan) resumeDeliveredText(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	idOutput, err := plan.body.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	key, err := readPlannedString(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	id, err := readPlannedString(idOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	item, tracker, err := state.runtime.deliveredItem(id)
	if err != nil {
		return plannedOutput{}, err
	}
	if state.runtime.outputStream != nil {
		state.budget.hasReadOutputStream = true
	}
	if item == nil || item.isFinished || item.kind != TextNode && item.kind != RefusalNode && item.kind != ReasoningNode {
		return plannedOutput{}, fmt.Errorf("text continuation requires an open delivered text item")
	}
	transaction := state.streamTransaction
	channel := streamTrackKey{channel: plan.expression.trackerName, item: key}
	if transaction.textUpdates[channel] != nil || transaction.primitives().texts[channel] != nil {
		return plannedOutput{}, fmt.Errorf("text tracker already has a prefix")
	}
	copy, err := cloneTextTracker(tracker)
	if err != nil {
		return plannedOutput{}, err
	}
	if err := transaction.reserveTrackChannel(key, state.budget.limits); err != nil {
		return plannedOutput{}, err
	}
	if transaction.textUpdates == nil {
		transaction.textUpdates = make(map[streamTrackKey]*pendingStreamText)
	}
	transaction.textUpdates[channel] = &pendingStreamText{tracker: copy}
	return plannedOutput{}, nil
}

func cloneTextTracker(tracker *TextTracker) (*TextTracker, error) {
	copy := &TextTracker{}
	if tracker == nil || tracker.digest == nil {
		return copy, nil
	}
	encoded, err := tracker.digest.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy.digest = sha256.New()
	if err := copy.digest.(encoding.BinaryUnmarshaler).UnmarshalBinary(encoded); err != nil {
		return nil, err
	}
	copy.length = tracker.length
	copy.scratch = new(textTrackerScratch)
	return copy, nil
}
