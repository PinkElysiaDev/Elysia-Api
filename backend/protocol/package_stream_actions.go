package protocol

import (
	"strconv"
)

// One frame shares immutable read caches across its ordered actions. Nested
// array members acquire only an active child reader, never a retained frame tree.
type streamActionFrame struct {
	state              *packageStreamValidation
	root               evaluation
	sequence           int64
	hasSequence        bool
	hasCheckedSequence bool
	hasAction          bool
	slots              []Value
	slotBytes          int
	hasStagedSlots     bool
	slotUpdates        []Value
}

func (state *packageStreamValidation) fieldReader(input evaluation) func(streamField) (Value, error) {
	return func(field streamField) (Value, error) {
		selected := input
		if field.fromRoot {
			selected.input = input.root
		}
		if field.value != nil {
			value, err := field.value.evaluate(selected)
			if err != nil {
				return Value{}, state.issue(field.path, "stream field expression failed", err)
			}
			return value, nil
		}
		value, err := readValuePointerWithObjectReader(selected.input, field.pointer, input.valueReader, input.objectReader)
		if err != nil {
			return Value{}, state.issue(field.path, "stream selector cannot read its declared field", err)
		}
		return value, nil
	}
}

func (state *packageStreamValidation) matches(condition *compiledOutputPlan, input evaluation, path string) (bool, error) {
	if condition == nil {
		return true, nil
	}
	value, err := condition.evaluate(input)
	if err != nil {
		return false, state.issue(path, "stream action condition failed", err)
	}
	isApplicable, err := readBoolean(value)
	if err != nil {
		return false, state.issue(path, "stream action condition must produce a boolean", err)
	}
	return isApplicable, nil
}

func (frame *streamActionFrame) consume(actions []compiledPackageStreamEvent, input evaluation, path string) error {
	for _, action := range actions {
		input.streamSlots = frame.slots
		read := frame.state.fieldReader(input)
		if err := input.advance(); err != nil {
			return err
		}
		if action.isAssertion || action.isStateUpdate {
			if err := frame.checkSequence(); err != nil {
				return err
			}
		}
		isApplicable, err := frame.state.matches(action.when, input, path+frame.state.contract.discriminator.path)
		if err != nil {
			return err
		}
		if !isApplicable {
			continue
		}
		action = qualifyStreamAction(action, path)
		if action.isAssertion || action.isStateUpdate {
			if frame.state.accepted.isTerminal && !action.canFollowTerminal {
				return frame.state.issue(frame.state.contract.discriminator.path, "validation action arrived after the declared terminal", nil)
			}
			var err error
			if action.isAssertion {
				err = validateWireAssertions(action.assertions, input, path, frame.state.direction)
			} else {
				err = frame.stageSlots(action.updates, input)
			}
			if err != nil {
				return err
			}
			frame.hasAction = true
			continue
		}
		if action.foreach.path != "" {
			if err := frame.consumeArray(action, input, read); err != nil {
				return err
			}
			frame.hasAction = true
			continue
		}
		if err := frame.state.checkActionPosition(action); err != nil {
			return err
		}
		responseID, err := frame.responseIdentity(action, read)
		if err != nil {
			return err
		}
		if err := frame.checkSequence(); err != nil {
			return err
		}
		if err := frame.state.consumeAction(input.ctx, action, responseID, read, input.valueReader); err != nil {
			return err
		}
		frame.hasAction = true
	}
	return nil
}

func (frame *streamActionFrame) consumeArray(action compiledPackageStreamEvent, input evaluation, read func(streamField) (Value, error)) error {
	value, err := read(action.foreach)
	if err != nil {
		return err
	}
	if value.IsZero() {
		return frame.checkSequence()
	}
	if value.IsNull() {
		return frame.state.issue(action.foreach.path, "stream iteration requires an array", nil)
	}
	items, err := value.readArrayWithin(input.valueReader)
	if err != nil {
		return frame.state.issue(action.foreach.path, "stream iteration requires an array", err)
	}
	for index, item := range items {
		child := input
		child.input, child.item, child.itemIndex = item, item, index
		if action.canBindItem {
			child.bindings[action.itemBinding] = JSONLeaf(item)
		}
		if action.canBindIndex {
			child.bindings[action.indexBinding] = JSONLeaf(coreInteger(int64(index)))
		}
		reader := planObjectReader{root: spanIdentity(item.raw), parent: input.objectReader}
		child.objectReader = &reader
		err := frame.consume(action.actions, child, action.foreach.path+"/"+strconv.Itoa(index))
		reader.releaseItem(err)
		if err != nil {
			return err
		}
	}
	return frame.checkSequence()
}

func (frame *streamActionFrame) responseIdentity(action compiledPackageStreamEvent, read func(streamField) (Value, error)) (string, error) {
	if action.isResponseIndependent {
		return "", nil
	}
	if action.canPrecedeResponse && (action.action == wireFail || action.action == wireCancel) {
		return frame.state.accepted.responseID, nil
	}
	if action.isControl {
		value, err := read(action.responseID)
		if err != nil || value.IsZero() {
			return "", err
		}
		identity, err := frame.state.readIdentityValue(value, action.responseID.path)
		if err != nil {
			return "", err
		}
		if frame.state.accepted.responseID != "" && frame.state.accepted.responseID != identity {
			return "", frame.state.issue(action.responseID.path, "control frame changed the response identity", nil)
		}
		return identity, nil
	}
	return frame.state.readResponseIdentity(action.responseID, read)
}

func (frame *streamActionFrame) checkSequence() error {
	field := frame.state.contract.sequence
	if frame.hasCheckedSequence || field.path == "" {
		return nil
	}
	value, err := frame.state.fieldReader(frame.root)(field)
	if err != nil {
		return err
	}
	if value.IsZero() && frame.state.contract.isSequenceOptional {
		frame.hasCheckedSequence = true
		return nil
	}
	sequence, err := frame.state.readCount(value, field.path)
	if err != nil {
		if frame.state.contract.sequenceDiagnostic == nil {
			return err
		}
		return frame.state.sequenceIssue("stream sequence must be a nonnegative integer", err)
	}
	if frame.state.accepted.hasSequence && sequence <= frame.state.accepted.sequence {
		return frame.state.sequenceIssue("stream sequence must increase strictly", nil)
	}
	frame.sequence, frame.hasSequence = sequence, true
	frame.hasCheckedSequence = true
	return nil
}

func (state *packageStreamValidation) sequenceIssue(reason string, cause error) error {
	if state.contract.sequenceReason != "" {
		reason = state.contract.sequenceReason
	}
	return state.boundaryIssue(state.contract.sequenceDiagnostic, state.contract.sequence.path, reason, cause)
}

func qualifyStreamAction(action compiledPackageStreamEvent, path string) compiledPackageStreamEvent {
	if path == "" {
		return action
	}
	qualify := func(field *streamField) {
		if field.path != "" && !field.fromRoot {
			field.path = path + field.path
		}
	}
	for _, field := range []*streamField{&action.foreach, &action.responseID, &action.key, &action.parent, &action.ordinal, &action.offset, &action.properties, &action.childrenCount, &action.delta, &action.failure} {
		qualify(field)
	}
	if len(action.identities) > 0 {
		selections := make([]compiledEntityIdentitySelection, len(action.identities))
		copy(selections, action.identities)
		for index := range selections {
			qualify(&selections[index].field)
		}
		action.identities = selections
	}
	return action
}
