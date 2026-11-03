package protocol

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Wire state owns external identities and payload validators, never core events.
type packageStreamValidation struct {
	retention                                   streamReservation
	contract                                    *compiledPackageStreamContract
	target                                      Target
	direction                                   Direction
	limits                                      Limits
	entities                                    []map[string]*wireEntityState
	entityCount, openItems                      int
	identityBytes, snapshotBytes, bufferedBytes int
	slots                                       []Value
	slotBytes, pendingSlotBytes                 int
	hasPendingSlots                             bool
	identities                                  []string
	identityBindings                            []streamIdentityBindings
	identityKeys                                []map[string]bool
	identityItems                               int
	rangeItems, rangeBytes                      int
	entityIdentityKeys                          map[string]map[string]*wireEntityState
	accepted, delivered                         streamPosition
	hasPendingDelivery                          bool
	frameNumber, deliveredFrameNumber           uint64
	hasRejectedFrame                            bool
	frameClass                                  wireFrameClass
	frameHadResponse, frameHasSequence          bool
	frameResponseID                             string
	frameSequence                               int64
	entityOrder                                 []*wireEntityState
}
type streamPosition struct {
	responseID                          string
	sequence                            int64
	hasSequence, hasStarted, isTerminal bool
	maxItemIndex                        int64
	hasItemIndex                        bool
}

func newPackageStreamValidation(contract *compiledPackageStreamContract, target Target, limits Limits) (*packageStreamValidation, error) {
	state := &packageStreamValidation{contract: contract, target: target, direction: target.Direction, limits: limits,
		identities: make([]string, len(contract.identities)), entities: make([]map[string]*wireEntityState, len(contract.namespaces))}
	if contract.state != nil {
		state.slots, state.slotBytes = contract.state.initial, contract.state.initialBytes
	}
	return state, nil
}
func (state *packageStreamValidation) consume(ctx context.Context, frame Value) error {
	return state.consumeInputView(ctx, newInputView(frame))
}
func (state *packageStreamValidation) consumeInputView(ctx context.Context, view *inputView) (err error) {
	if state.hasRejectedFrame {
		return state.issue("/", "rejected stream state cannot accept another frame", nil)
	}
	defer func() {
		state.pendingSlotBytes, state.hasPendingSlots = 0, false
		if err != nil {
			state.hasRejectedFrame = true
		}
	}()
	state.frameNumber++
	state.frameClass = wireFrameOrdinary
	prior := state.accepted
	frame := view.value
	conditions := evaluation{ctx: ctx, input: frame, root: frame, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), streamSlots: state.slots,
		budget: &evaluationBudget{limits: state.limits, stepLimit: state.limits.Nodes}}
	if state.contract.bindingSlots != 0 {
		conditions.bindings = make([]JSONOutput, state.contract.bindingSlots)
	}
	tag, err := state.readIdentity(state.contract.discriminator, state.fieldReader(conditions))
	if err != nil {
		return err
	}
	actions, exists := state.contract.events[tag]
	if !exists {
		return state.issue(state.contract.discriminator.path, "undeclared stream event tag", nil)
	}
	if err := state.bindIdentities(conditions); err != nil {
		return err
	}
	current := streamActionFrame{state: state, root: conditions, sequence: state.accepted.sequence, slots: state.slots, slotBytes: state.slotBytes}
	if err := current.consume(actions, conditions, ""); err != nil {
		return err
	}
	if !current.hasAction {
		return state.issue(state.contract.discriminator.path, "stream event does not match a declared action", nil)
	}
	state.accepted.sequence, state.accepted.hasSequence = current.sequence, state.accepted.hasSequence || current.hasSequence
	if current.hasStagedSlots {
		state.slots, state.slotBytes = current.slots, current.slotBytes
	}
	if err := state.commitRetention(); err != nil {
		return state.issue("/state", "stream owners exceed their shared budget", err)
	}
	state.hasPendingDelivery = state.direction == EncodeEvent
	state.frameHadResponse, state.frameResponseID = prior.hasStarted, prior.responseID
	state.frameHasSequence, state.frameSequence = current.hasSequence, current.sequence
	return nil
}
func (state *packageStreamValidation) checkActionPosition(action compiledPackageStreamEvent) error {
	if state.accepted.isTerminal && !action.canFollowTerminal {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "event arrived after the declared terminal", nil)
	}
	if action.action == wireStart && state.accepted.hasStarted && !action.isImplicit {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "stream started more than once", nil)
	}
	if action.action != wireStart && !action.canPrecedeResponse && !state.accepted.hasStarted {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "event requires a preceding stream start", nil)
	}
	return nil
}
func (state *packageStreamValidation) consumeAction(ctx context.Context, action compiledPackageStreamEvent, responseID string, read func(streamField) (Value, error), reader *valueReader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.frameClass != wireFrameOrdinary {
		if state.frameClass != wireFrameOrdinary && state.frameClass != action.frameClass {
			return state.issue(state.contract.discriminator.path, "frame declares conflicting classes", nil)
		}
		state.frameClass = action.frameClass
	}
	if action.action >= wireEntityStart && action.action <= wireEntityFinish {
		if err := state.consumeEntity(ctx, action, read, reader); err != nil {
			return err
		}
	} else {
		switch action.action {
		case wireFinish:
			if err := state.finishEntities(ctx, action); err != nil {
				return err
			}
		case wireFail:
			value, err := read(action.failure)
			if err != nil {
				return err
			}
			if value.IsZero() || value.IsNull() {
				return state.issue(action.failure.path, "failure event requires an error", nil)
			}
		}
	}
	if !action.isControl && !action.isResponseIndependent && state.accepted.responseID == "" && responseID != "" {
		if err := state.checkRetainedBytes(state.identityBytes+len(responseID), action.responseID.path); err != nil {
			return err
		}
		state.accepted.responseID = strings.Clone(responseID)
		state.identityBytes += len(responseID)
	}
	if action.action == wireStart {
		state.accepted.hasStarted = true
	}
	if action.action == wireFinish || action.action == wireFail || action.action == wireCancel {
		state.accepted.isTerminal = true
		// No payload-bearing action is valid after a terminal, including delivery retries.
		for _, entity := range state.entityOrder {
			entity.json.Reset()
			entity.array = nil
			entity.arrayBytes, entity.arrayNodes = 0, 0
		}
		state.bufferedBytes = 0
	}
	return nil
}
func (state *packageStreamValidation) retainedSlotBytes() int {
	if state.hasPendingSlots {
		return state.pendingSlotBytes
	}
	return state.slotBytes
}

func (state *packageStreamValidation) checkRetainedItems(additional int, path string) error {
	items := state.retainedItems() + additional
	if items > state.limits.StateItems {
		return state.issue(path, "stream retained state exceeds the item limit", resourceFailure("stream", "state_items", state.limits.StateItems, items))
	}
	return nil
}
func (state *packageStreamValidation) checkRetainedBytes(identityBytes int, path string) error {
	return state.checkRetainedState(identityBytes, state.snapshotBytes, path)
}
func (state *packageStreamValidation) checkRetainedState(identityBytes, snapshotBytes int, path string) error {
	return state.checkRetainedStateWithSlots(identityBytes, snapshotBytes, state.retainedSlotBytes(), path)
}
func (state *packageStreamValidation) checkRetainedStateWithSlots(identityBytes, snapshotBytes, slotBytes int, path string) error {
	retained := identityBytes + snapshotBytes + slotBytes + state.bufferedBytes + state.rangeBytes
	if retained <= state.limits.BufferBytes {
		return nil
	}
	metric, observed := "state_bytes", retained
	if identityBytes > state.limits.BufferBytes {
		metric, observed = "identity_bytes", identityBytes
	}
	return state.issue(path, "stream retained state exceeds the buffer limit", resourceFailure("stream", metric, state.limits.BufferBytes, observed))
}
func (state *packageStreamValidation) readResponseIdentity(field streamField, read func(streamField) (Value, error)) (string, error) {
	value, err := read(field)
	if err != nil {
		return "", err
	}
	if value.IsZero() && state.contract.canInheritResponseID && state.accepted.hasStarted {
		return state.accepted.responseID, nil
	}
	if state.contract.isResponseIDOptional && (value.IsZero() || value.IsNull() || value.IsString("")) {
		return state.accepted.responseID, nil
	}
	identity, err := state.readIdentityValue(value, field.path)
	if err != nil {
		return "", err
	}
	if state.accepted.hasStarted && state.accepted.responseID != "" && identity != state.accepted.responseID {
		return "", state.issue(field.path, "response identity changed", nil)
	}
	return identity, nil
}

func (state *packageStreamValidation) readIdentity(field streamField, read func(streamField) (Value, error)) (string, error) {
	value, err := read(field)
	if err != nil {
		return "", err
	}
	return state.readIdentityValue(value, field.path)
}

func (state *packageStreamValidation) readIdentityValue(value Value, path string) (string, error) {
	text, err := readString(value)
	if err != nil || text == "" {
		return "", state.issue(path, "stream identity must be a nonempty string", err)
	}
	return text, nil
}

func (state *packageStreamValidation) readCount(value Value, path string) (int64, error) {
	count, err := strconv.ParseInt(strings.TrimSpace(value.raw), 10, 64)
	if err != nil || count < 0 {
		return 0, state.issue(path, "stream counter must be a nonnegative integer", err)
	}
	return count, nil
}

func (state *packageStreamValidation) finish() error {
	if state.hasRejectedFrame {
		return state.issue("/", "rejected stream state cannot finish", nil)
	}
	if !state.accepted.isTerminal {
		return state.issue(state.contract.discriminator.path, "stream ended before its declared terminal", nil)
	}
	return nil
}
func (state *packageStreamValidation) issue(path, reason string, cause error) error {
	code := wireBoundaryCode(state.direction)
	var limit *ResourceLimitError
	if errors.As(cause, &limit) {
		code = LimitExceeded
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: state.target.Protocol, Direction: state.direction, Stage: "wire.stream", Path: path, Reason: reason}}}
}
