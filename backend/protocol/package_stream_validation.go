package protocol

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// One response owns this state. The contract and mapping share no mutable state;
// only identities, cumulative usage and text digests survive the current frame.
type packageStreamValidation struct {
	contract           *compiledPackageStreamContract
	replay             *EventReplay
	target             Target
	direction          Direction
	openItems          int
	identityBytes      int
	accepted           streamPosition
	delivered          streamPosition
	hasPendingDelivery bool
}

type streamPosition struct {
	responseID  string
	sequence    int64
	hasSequence bool
	hasStarted  bool
	isTerminal  bool
}

func newPackageStreamValidation(contract *compiledPackageStreamContract, target Target, limits Limits) (*packageStreamValidation, error) {
	replay, err := NewEventReplay(target, limits)
	if err != nil {
		return nil, err
	}
	return &packageStreamValidation{contract: contract, replay: replay, target: target, direction: target.Direction}, nil
}

func (state *packageStreamValidation) consume(ctx context.Context, frame Value) error {
	reader := newNativeComparisonReader(frame)
	objects := &planObjectReader{root: spanIdentity(frame.raw)}
	read := func(field streamField) (Value, error) {
		value, err := readValuePointerWithObjectReader(frame, field.pointer, reader, objects)
		if err != nil {
			return Value{}, state.issue(field.path, "stream selector cannot read its declared field", err)
		}
		return value, nil
	}
	tag, err := state.readIdentity(state.contract.discriminator, read)
	if err != nil {
		return err
	}
	definition, exists := state.contract.events[tag]
	if !exists {
		return state.issue(state.contract.discriminator.path, "undeclared stream event tag", nil)
	}
	if state.replay.isTerminal && definition.action != UsageUpdated {
		return state.issue(state.contract.discriminator.path, "event arrived after terminal; only usage tails are permitted", nil)
	}
	if definition.action == ResponseStarted && state.accepted.hasStarted {
		return state.issue(state.contract.discriminator.path, "response started more than once", nil)
	}
	if definition.action != ResponseStarted && !state.accepted.hasStarted {
		return state.issue(state.contract.discriminator.path, "event requires a preceding response start", nil)
	}
	responseID, err := state.readIdentity(state.contract.responseID, read)
	if err != nil {
		return err
	}
	if state.accepted.hasStarted && responseID != state.accepted.responseID {
		return state.issue(state.contract.responseID.path, "response identity changed", nil)
	}
	sequence := state.accepted.sequence
	if state.contract.sequence.path != "" {
		value, err := read(state.contract.sequence)
		if err != nil {
			return err
		}
		sequence, err = state.readCount(value, state.contract.sequence.path)
		if err != nil {
			return err
		}
		if state.accepted.hasSequence && sequence <= state.accepted.sequence {
			return state.issue(state.contract.sequence.path, "stream sequence must increase strictly", nil)
		}
	}
	event := Event{SchemaVersion: state.target.coreSchemaVersion(), Source: state.target.Protocol, Type: definition.action}
	identityBytes := state.identityBytes
	if !state.accepted.hasStarted {
		identityBytes += len(responseID)
	}
	itemBytes, err := state.readPayload(&event, &definition, read)
	if err != nil {
		return err
	}
	identityBytes += itemBytes
	// Bound retained identity bytes as well as identity count. This is a logical
	// payload budget, not an estimate of Go map or allocator overhead.
	if identityBytes > state.replay.limits.BufferBytes {
		return state.issue(state.contract.itemID.path, "stream identities exceed the buffer limit", resourceFailure("stream", "identity_bytes", state.replay.limits.BufferBytes, identityBytes))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := state.replay.Consume(event); err != nil {
		return state.replayError(err, definition)
	}
	if !state.accepted.hasStarted {
		state.accepted.responseID = responseID
		state.accepted.hasStarted = true
	}
	state.identityBytes = identityBytes
	state.accepted.sequence, state.accepted.hasSequence = sequence, state.contract.sequence.path != ""
	switch definition.action {
	case ItemStarted:
		state.openItems++
	case ItemFinished:
		state.openItems--
	case ResponseFinished, OperationFailed, OperationCancelled:
		state.accepted.isTerminal = true
	}
	state.hasPendingDelivery = state.direction == EncodeEvent
	return nil
}

func (state *packageStreamValidation) readPayload(event *Event, definition *compiledPackageStreamEvent, read func(streamField) (Value, error)) (int, error) {
	switch event.Type {
	case ItemStarted, ItemDelta, ItemFinished:
		itemID, err := state.readIdentity(state.contract.itemID, read)
		if err != nil {
			return 0, err
		}
		event.ItemID = StringValue(itemID)
		if event.Type == ItemStarted {
			event.Item = &Node{Kind: TextNode}
			return len(itemID), nil
		}
		if event.Type == ItemDelta {
			event.Delta, err = read(definition.text)
			if err != nil {
				return 0, err
			}
			if !event.Delta.IsStringValue() {
				return 0, state.issue(definition.text.path, "text delta must be a string", nil)
			}
		}
	case UsageUpdated:
		var err error
		event.Usage, err = state.readUsage(definition.usage[:], read)
		return 0, err
	case ResponseFinished:
		if state.openItems != 0 {
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

func (state *packageStreamValidation) readUsage(fields []streamField, read func(streamField) (Value, error)) (*Usage, error) {
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
	if usage.Input == nil && usage.Output == nil && usage.Total == nil {
		return nil, state.issue(state.contract.discriminator.path, "usage event requires at least one observed counter", nil)
	}
	return usage, nil
}

func (state *packageStreamValidation) readIdentity(field streamField, read func(streamField) (Value, error)) (string, error) {
	value, err := read(field)
	if err != nil {
		return "", err
	}
	text, err := readString(value)
	if err != nil || text == "" {
		return "", state.issue(field.path, "stream identity must be a nonempty string", err)
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
	return state.replayError(state.replay.Finish(), compiledPackageStreamEvent{})
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
			issue.Path = state.contract.itemID.path
		case "/delta":
			issue.Path = event.text.path
		case "/error":
			issue.Path = event.failure.path
		case "/usage":
			issue.Path = "/"
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
