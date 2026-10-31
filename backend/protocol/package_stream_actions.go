package protocol

import (
	"strconv"
	"strings"
)

// One frame shares immutable read caches across its ordered actions. Nested
// array members acquire only an active child reader, never a retained frame tree.
type streamActionFrame struct {
	state       *packageStreamValidation
	root        evaluation
	sequence    int64
	hasSequence bool
	hasAction   bool
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
				return Value{}, state.issue(field.path, "stream identity expression failed", err)
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
	read := frame.state.fieldReader(input)
	for _, action := range actions {
		if err := input.advance(); err != nil {
			return err
		}
		isApplicable, err := frame.state.matches(action.when, input, path+frame.state.contract.discriminator.path)
		if err != nil {
			return err
		}
		if !isApplicable {
			continue
		}
		action = qualifyStreamAction(action, path)
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
		if err := frame.state.consumeAction(input.ctx, action, responseID, read); err != nil {
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
		child.input = item
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
	if action.canPrecedeResponse && (action.action == OperationFailed || action.action == OperationCancelled) {
		return frame.state.accepted.responseID, nil
	}
	if action.isControl {
		value, err := read(action.responseID)
		if err != nil || value.IsZero() {
			return "", err
		}
	}
	return frame.state.readResponseIdentity(action.responseID, read)
}

func (frame *streamActionFrame) checkSequence() error {
	field := frame.state.contract.sequence
	if frame.hasSequence || field.path == "" {
		return nil
	}
	value, err := frame.state.fieldReader(frame.root)(field)
	if err != nil {
		return err
	}
	sequence, err := frame.state.readCount(value, field.path)
	if err != nil {
		return err
	}
	if frame.state.accepted.hasSequence && sequence <= frame.state.accepted.sequence {
		return frame.state.issue(field.path, "stream sequence must increase strictly", nil)
	}
	frame.sequence, frame.hasSequence = sequence, true
	return nil
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
	for _, field := range []*streamField{&action.foreach, &action.responseID, &action.itemID, &action.delta, &action.failure, &action.usage[0], &action.usage[1], &action.usage[2]} {
		qualify(field)
	}
	if action.tool != nil {
		tool := *action.tool
		qualify(&tool.name)
		qualify(&tool.callID)
		action.tool = &tool
	}
	return action
}

func (state *packageStreamValidation) bindIdentities(input evaluation) error {
	read := state.fieldReader(input)
	for index, identity := range state.contract.identities {
		isApplicable, err := state.matches(identity.when, input, identity.field.path)
		if err != nil {
			return err
		}
		if !isApplicable {
			continue
		}
		value, err := read(identity.field)
		if err != nil {
			return err
		}
		if value.IsZero() {
			continue
		}
		var normalized string
		if identity.kind == IntegerType {
			count, err := state.readCount(value, identity.field.path)
			if err != nil {
				return err
			}
			normalized = strconv.FormatInt(count, 10)
		} else {
			normalized, err = state.readIdentityValue(value, identity.field.path)
			if err != nil {
				return err
			}
		}
		if previous := state.identities[index]; previous != "" {
			if previous != normalized {
				return state.issue(identity.field.path, "stream identity changed after it was established", nil)
			}
			continue
		}
		if err := state.checkRetainedBytes(state.identityBytes+len(normalized), identity.field.path); err != nil {
			return err
		}
		state.identities[index] = strings.Clone(normalized)
		state.identityBytes += len(normalized)
	}
	return nil
}
