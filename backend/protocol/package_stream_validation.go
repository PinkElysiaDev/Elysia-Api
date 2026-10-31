package protocol

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// One response owns this state. The contract and mapping share no mutable state;
// Identities and digests survive each item. JSON tool arguments are retained
// only until tool completion or an unsuccessful response terminal.
type packageStreamValidation struct {
	contract             *compiledPackageStreamContract
	replay               *EventReplay
	target               Target
	direction            Direction
	openItems            int
	identityBytes        int
	snapshotBytes        int
	slots                []Value
	slotBytes            int
	pendingSlotBytes     int
	hasPendingSlots      bool
	identities           []string
	identityBindings     []streamIdentityBindings
	identityItems        int
	toolPaths            map[string]streamToolPaths
	accepted             streamPosition
	delivered            streamPosition
	hasPendingDelivery   bool
	itemSnapshots        map[string]*streamItemSnapshot
	frameNumber          uint64
	deliveredFrameNumber uint64
	hasRejectedFrame     bool
}

type streamPosition struct {
	responseID   string
	sequence     int64
	hasSequence  bool
	hasStarted   bool
	isTerminal   bool
	maxItemIndex int64
	hasItemIndex bool
}

func newPackageStreamValidation(contract *compiledPackageStreamContract, target Target, limits Limits) (*packageStreamValidation, error) {
	replay, err := NewEventReplay(target, limits)
	if err != nil {
		return nil, err
	}
	state := &packageStreamValidation{contract: contract, replay: replay, target: target, direction: target.Direction, identities: make([]string, len(contract.identities))}
	if contract.state != nil {
		state.slots, state.slotBytes = contract.state.initial, contract.state.initialBytes
	}
	return state, nil
}

func (state *packageStreamValidation) consume(ctx context.Context, frame Value) error {
	return state.consumeInputView(ctx, newInputView(frame))
}

func (state *packageStreamValidation) consumeInputView(ctx context.Context, view *inputView) error {
	defer func() {
		state.pendingSlotBytes, state.hasPendingSlots = 0, false
	}()
	state.frameNumber++
	frame := view.value
	conditions := evaluation{ctx: ctx, input: frame, root: frame, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(), streamSlots: state.slots,
		budget: &evaluationBudget{limits: state.replay.limits, stepLimit: state.replay.limits.Nodes}}
	if state.contract.bindingSlots != 0 {
		conditions.bindings = make([]JSONOutput, state.contract.bindingSlots)
	}
	read := state.fieldReader(conditions)
	tag, err := state.readIdentity(state.contract.discriminator, read)
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
	state.hasPendingDelivery = state.direction == EncodeEvent
	return nil
}

func (state *packageStreamValidation) checkActionPosition(action compiledPackageStreamEvent) error {
	if action.isControl {
		return nil
	}
	if state.replay.isTerminal && action.action != UsageUpdated {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "event arrived after terminal; only usage tails are permitted", nil)
	}
	if action.action == ResponseStarted && state.accepted.hasStarted && !action.isImplicit {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "response started more than once", nil)
	}
	if action.action != ResponseStarted && !action.canPrecedeResponse && !state.accepted.hasStarted {
		return state.boundaryIssue(action.diagnostic, state.contract.discriminator.path, "event requires a preceding response start", nil)
	}
	return nil
}

func (state *packageStreamValidation) consumeAction(ctx context.Context, definition compiledPackageStreamEvent, responseID string, read func(streamField) (Value, error)) error {
	if definition.isControl {
		return ctx.Err()
	}
	isRepeatedStart := definition.isImplicit && definition.action == ResponseStarted && state.accepted.hasStarted
	event := Event{SchemaVersion: state.target.coreSchemaVersion(), Source: state.target.Protocol, Type: definition.action}
	identityBytes := state.identityBytes
	if state.accepted.responseID == "" {
		identityBytes += len(responseID)
	}
	itemBytes, err := state.readPayload(&event, &definition, read)
	if err != nil {
		return err
	}
	if definition.isImplicit && definition.action == ItemStarted && !definition.isTool {
		itemID, _ := readString(event.ItemID)
		isRepeatedStart = state.replay.items[state.replay.identities.aliases["item:"+itemID]] != nil
	}
	identityBytes += itemBytes
	snapshot, snapshotBytes, skipReplay, err := state.prepareItemSnapshot(event, definition, read)
	if err != nil {
		return err
	}
	snapshotBytes += state.snapshotBytes
	isRepeatedStart = isRepeatedStart || skipReplay
	toolKey, toolPaths, pathBytes := state.prepareToolPaths(event, definition)
	identityBytes += pathBytes
	// Bound retained identity bytes as well as identity count. This is a logical
	// payload budget, not an estimate of Go map or allocator overhead.
	if err := state.checkRetainedState(identityBytes, snapshotBytes, state.contract.itemID.path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isRepeatedStart {
		// The replay buffer owns decoded tool arguments. Give it the remaining
		// retention allowance, without reducing frame or semantic input limits.
		bufferLimit := state.replay.state.limits.BufferBytes
		state.replay.state.limits.BufferBytes = state.replay.limits.BufferBytes - identityBytes - snapshotBytes - state.retainedSlotBytes()
		_, err := state.replay.Consume(event)
		state.replay.state.limits.BufferBytes = bufferLimit
		if err != nil {
			return state.replayError(err, definition)
		}
	}
	if state.accepted.responseID == "" {
		state.accepted.responseID = responseID
	}
	state.accepted.hasStarted = state.accepted.hasStarted || event.Type == ResponseStarted
	state.identityBytes = identityBytes
	state.snapshotBytes = snapshotBytes
	state.commitItemSnapshot(event, definition, snapshot)
	if toolKey != "" {
		if state.toolPaths == nil {
			state.toolPaths = make(map[string]streamToolPaths)
		}
		state.toolPaths[toolKey] = toolPaths
	}
	switch event.Type {
	case ItemStarted:
		if !isRepeatedStart {
			state.openItems++
			if state.contract.isItemIDIndex {
				id, _ := readString(event.ItemID)
				index, _ := strconv.ParseInt(id, 10, 64)
				state.accepted.maxItemIndex = max(state.accepted.maxItemIndex, index)
				state.accepted.hasItemIndex = true
			}
		}
	case ItemFinished:
		if !skipReplay {
			state.openItems--
		}
	case ResponseFinished, OperationFailed, OperationCancelled:
		state.accepted.isTerminal = true
		if state.contract.closeItemsOnFinish {
			state.openItems = 0
		}
	}
	if state.contract.hasTools && (event.Type == OperationFailed || event.Type == OperationCancelled) {
		for _, tool := range state.replay.state.tools {
			tool.input.Reset()
		}
		state.replay.state.buffered = 0
	}
	return nil
}

func (state *packageStreamValidation) checkRetainedBytes(identityBytes int, path string) error {
	return state.checkRetainedState(identityBytes, state.snapshotBytes, path)
}

func (state *packageStreamValidation) checkRetainedState(identityBytes, snapshotBytes int, path string) error {
	return state.checkRetainedStateWithSlots(identityBytes, snapshotBytes, state.retainedSlotBytes(), path)
}

func (state *packageStreamValidation) retainedSlotBytes() int {
	if state.hasPendingSlots {
		return state.pendingSlotBytes
	}
	return state.slotBytes
}

func (state *packageStreamValidation) checkRetainedStateWithSlots(identityBytes, snapshotBytes, slotBytes int, path string) error {
	retained := identityBytes + snapshotBytes + slotBytes + state.replay.state.buffered
	limit := state.replay.limits.BufferBytes
	if retained <= limit {
		return nil
	}
	metric, observed, reason := "state_bytes", retained, "stream retained state exceeds the buffer limit"
	if identityBytes > limit {
		// Preserve the established identity-only diagnostic and its cause;
		// state_bytes identifies a limit reached only by combining payloads.
		metric, observed, reason = "identity_bytes", identityBytes, "stream identities exceed the buffer limit"
	}
	return state.issue(path, reason, resourceFailure("stream", metric, limit, observed))
}

func (state *packageStreamValidation) readPayload(event *Event, definition *compiledPackageStreamEvent, read func(streamField) (Value, error)) (int, error) {
	switch event.Type {
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		return state.readItemPayload(event, definition, read)
	case UsageUpdated:
		var err error
		event.Usage, err = state.readUsage(definition.usage[:], definition.canOmitUsageCounters, read)
		return 0, err
	case ResponseFinished:
		if err := state.checkRequiredItems(*definition); err != nil {
			return 0, err
		}
		if state.openItems != 0 && !state.contract.closeItemsOnFinish {
			if state.contract.hasTools {
				return 0, state.issue(state.contract.discriminator.path, "response finished before all stream items", nil)
			}
			return 0, state.issue(state.contract.discriminator.path, "response finished before all text items", nil)
		}
	case OperationFailed:
		var err error
		event.Error, err = read(definition.failure)
		if err != nil {
			return 0, err
		}
		if event.Error.IsZero() || event.Error.IsNull() {
			return 0, state.issue(definition.failure.path, "failure event requires an error", nil)
		}
	}
	return 0, nil
}

func (state *packageStreamValidation) readToolIdentity(field streamField, isSparse bool, read func(streamField) (Value, error)) (Value, int, error) {
	value, err := read(field)
	if err != nil || isSparse && value.IsZero() {
		return Value{}, 0, err
	}
	text, err := readString(value)
	if err != nil || !isSparse && text == "" {
		return Value{}, 0, state.issue(field.path, "tool identity must be a string", err)
	}
	return StringValue(text), len(text), nil
}

func (state *packageStreamValidation) readUsage(fields []streamField, canOmitCounters bool, read func(streamField) (Value, error)) (*Usage, error) {
	usage := &Usage{}
	for index, counter := range []**Counter{&usage.Input, &usage.Output, &usage.Total} {
		field := fields[index]
		if field.path == "" {
			continue
		}
		value, err := read(field)
		if err != nil {
			return nil, err
		}
		if value.IsZero() {
			continue
		}
		count, err := state.readCount(value, field.path)
		if err != nil {
			return nil, err
		}
		*counter = &Counter{Count: count, Origin: ObservedCount}
	}
	if !canOmitCounters && usage.Input == nil && usage.Output == nil && usage.Total == nil {
		return nil, state.issue(state.contract.discriminator.path, "usage event requires at least one observed counter", nil)
	}
	return usage, nil
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

func (state *packageStreamValidation) readItemIdentity(field streamField, read func(streamField) (Value, error)) (string, error) {
	if !state.contract.isItemIDIndex {
		return state.readIdentity(field, read)
	}
	value, err := read(field)
	if err != nil {
		return "", err
	}
	index, err := state.readCount(value, field.path)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(index, 10), nil
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
	return state.replayError(state.replay.Finish(), compiledPackageStreamEvent{itemID: state.contract.itemID})
}

func (state *packageStreamValidation) issue(path, reason string, cause error) error {
	code := ConversionRejected
	if state.direction == DecodeEvent {
		code = UpstreamContractViolation
	}
	var limit *ResourceLimitError
	if errors.As(cause, &limit) {
		code = LimitExceeded
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: state.target.Protocol, Direction: state.direction, Stage: "wire.stream", Path: path, Reason: reason}}}
}

func (state *packageStreamValidation) replayError(err error, event compiledPackageStreamEvent) error {
	if err == nil {
		return nil
	}
	var conversion *ConversionError
	if !errors.As(err, &conversion) {
		return state.issue("/", "stream lifecycle validation failed", err)
	}
	issues := make([]ConversionIssue, len(conversion.Issues))
	for index, issue := range conversion.Issues {
		issue.Protocol, issue.Direction, issue.Stage = state.target.Protocol, state.direction, "wire.stream"
		switch issue.Path {
		case "/type":
			issue.Path = state.contract.discriminator.path
		case "/item", "/itemId", "/item/kind":
			issue.Path = event.itemID.path
		case "/delta", "/item/input/value":
			issue.Path = event.delta.path
		case "snapshot":
			if event.action == ItemSnapshot {
				issue.Path = event.delta.path
			}
		case "/item/name":
			issue.Path = event.itemID.path
			if event.tool != nil {
				issue.Path = event.tool.name.path
			}
		case "/item/callId", "/callId":
			issue.Path = event.itemID.path
			if event.tool != nil {
				issue.Path = event.tool.callID.path
			}
		case "/error":
			issue.Path = event.failure.path
		case "/usage":
			issue.Path = "/"
		}
		if item := state.replay.items[issue.Path]; item != nil && item.kind == ToolCallNode {
			paths := state.toolPaths[issue.Path]
			switch event.action {
			case ItemStarted:
				issue.Path = event.tool.callID.path
			case ItemDelta, ItemSnapshot:
				issue.Path = event.delta.path
			case ItemFinished:
				issue.Path = event.itemID.path
			case ResponseFinished:
				switch {
				case item.name == "":
					issue.Path = paths.name
				case item.callID == "":
					issue.Path = paths.callID
				default:
					issue.Path = paths.arguments
				}
			}
		}
		if issue.Code != LimitExceeded {
			issue.Code = ConversionRejected
			if state.direction == DecodeEvent {
				issue.Code = UpstreamContractViolation
			}
		}
		issues[index] = issue
	}
	return &ConversionError{cause: err, Issues: issues}
}
