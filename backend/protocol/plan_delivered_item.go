package protocol

import (
	"crypto/sha256"
	"encoding"
	"fmt"
	"strconv"
)

// Delivered items are borrowed only while validation has no newer, unsent
// state. Resuming copies only the state required to continue encoding;
// native-only streams keep no renderer item or second tracker.
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

func (runtime evaluationRuntime) deliveredItem(namespace, id string) (*wireEntityState, *TextTracker, error) {
	state, err := runtime.deliveredItems()
	if err != nil || state == nil {
		return nil, nil, err
	}
	slot, exists := state.contract.namespaceSlots[namespace]
	if !exists {
		return nil, nil, fmt.Errorf("unknown delivered namespace %q", namespace)
	}
	item := state.entities[slot][id]
	if item == nil {
		return nil, nil, nil
	}
	return item, &item.text, nil
}

func (plan *compiledOutputPlan) readDeliveredItem(state evaluation) (plannedOutput, error) {
	selected, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	id, err := readPlannedString(selected.output)
	if err != nil {
		return plannedOutput{}, err
	}
	item, _, err := state.runtime.deliveredItem(plan.expression.streamNamespace, id)
	if err == nil && state.runtime.outputStream != nil {
		state.budget.hasReadOutputStream = true
	}
	if err != nil || item == nil {
		return plannedOutput{}, err
	}
	contract := state.runtime.outputStream.contract
	definition := contract.entities[item.definition]
	fields := map[string]JSONOutput{
		"entity": JSONLeaf(StringValue(definition.name)), "payload": JSONLeaf(StringValue(definition.payload)), "isFinished": JSONLeaf(coreBoolean(item.isFinished)),
	}
	if contract.namespaces[definition.namespace].keyType == IntegerType {
		index, _ := strconv.ParseInt(id, 10, 64)
		fields["index"] = JSONLeaf(coreInteger(index))
	}
	if item.parent != nil {
		parentDefinition := contract.entities[item.parent.definition]
		fields["parent"] = JSONObject(map[string]JSONOutput{"namespace": JSONLeaf(StringValue(contract.namespaces[parentDefinition.namespace].name)), "key": JSONLeaf(StringValue(item.parent.key))})
	}
	if item.hasOrdinal {
		fields["ordinal"] = JSONLeaf(coreInteger(item.ordinal))
	}
	if !item.properties.IsZero() {
		fields["properties"] = JSONLeaf(item.properties)
	}
	if len(item.identities) > 0 {
		identities := make(map[string]JSONOutput, len(item.identities))
		for slot, identity := range item.identities {
			if identity != "" {
				identities[definition.identities[slot].name] = JSONLeaf(StringValue(identity))
			}
		}
		fields["identities"] = JSONObject(identities)
	}
	output := JSONObject(fields)
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (plan *compiledOutputPlan) selectDeliveredContinuation(state evaluation) (string, *wireEntityState, error) {
	keyOutput, err := plan.source.buildNode(state)
	if err != nil {
		return "", nil, err
	}
	idOutput, err := plan.body.buildNode(state)
	if err != nil {
		return "", nil, err
	}
	key, err := readPlannedString(keyOutput.output)
	if err != nil {
		return "", nil, err
	}
	id, err := readPlannedString(idOutput.output)
	if err != nil {
		return "", nil, err
	}
	item, _, err := state.runtime.deliveredItem(plan.expression.streamNamespace, id)
	if err != nil {
		return "", nil, err
	}
	if state.runtime.outputStream != nil {
		state.budget.hasReadOutputStream = true
	}
	if item == nil || item.isFinished {
		return "", nil, fmt.Errorf("continuation requires an open delivered entity")
	}
	return key, item, nil
}

func (plan *compiledOutputPlan) resumeDeliveredText(state evaluation) (plannedOutput, error) {
	key, item, err := plan.selectDeliveredContinuation(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if state.runtime.outputStream.contract.entities[item.definition].payload != "text" {
		return plannedOutput{}, fmt.Errorf("text continuation requires a text entity")
	}
	transaction := state.streamTransaction
	channel := streamTrackKey{channel: plan.expression.trackerName, item: key}
	if transaction.textUpdates[channel] != nil || transaction.primitives().texts[channel] != nil {
		return plannedOutput{}, fmt.Errorf("text tracker already has a prefix")
	}
	copy, err := cloneTextTracker(&item.text)
	if err != nil {
		return plannedOutput{}, err
	}
	if err := transaction.reserveTrackChannel(key); err != nil {
		return plannedOutput{}, err
	}
	if transaction.textUpdates == nil {
		transaction.textUpdates = make(map[streamTrackKey]*pendingStreamText)
	}
	transaction.textUpdates[channel] = &pendingStreamText{tracker: copy}
	return plannedOutput{}, nil
}

func (plan *compiledOutputPlan) resumeDeliveredTool(state evaluation) (plannedOutput, error) {
	key, item, err := plan.selectDeliveredContinuation(state)
	if err != nil {
		return plannedOutput{}, err
	}
	definition := state.runtime.outputStream.contract.entities[item.definition]
	if definition.payload != "json" || definition.isArray {
		return plannedOutput{}, fmt.Errorf("tool continuation requires a JSON entity")
	}
	transaction := state.streamTransaction
	channel := streamTrackKey{channel: plan.expression.trackerName, item: key}
	if transaction.toolUpdates[channel] != nil || transaction.primitives().tools[channel] != nil {
		return plannedOutput{}, fmt.Errorf("tool tracker already has a prefix")
	}
	prefix := item.json.String()
	if err := transaction.checkRetainedBytes(len(prefix)); err != nil {
		return plannedOutput{}, err
	}
	transaction.toolBufferDelta += len(prefix)
	if err := transaction.reserveTrackChannel(key); err != nil {
		return plannedOutput{}, err
	}
	// Snapshots and completion need the JSON prefix. Acquire it once when
	// semantic rendering starts; later native frames update this same tracker.
	tool := &streamTrackedTool{kind: JSONInput, emitted: len(prefix), isPlaceholder: item.isPlaceholder}
	tool.input.WriteString(prefix)
	tool.text.Append(prefix)
	if transaction.toolUpdates == nil {
		transaction.toolUpdates = make(map[streamTrackKey]*pendingStreamTool)
	}
	transaction.toolUpdates[channel] = &pendingStreamTool{tool: tool, kind: JSONInput, emitted: len(prefix), retainedBytes: len(prefix)}
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
