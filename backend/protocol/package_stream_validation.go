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
	conditions := evaluation{ctx: ctx, input: frame, root: frame, valueReader: reader, objectReader: objects,
		budget: &evaluationBudget{limits: state.replay.limits, stepLimit: state.replay.limits.Nodes}}
	read := func(field streamField) (Value, error) {
		if field.value != nil {
			value, err := field.value.evaluate(conditions)
			if err != nil {
				return Value{}, state.issue(field.path, "stream identity expression failed", err)
			}
			return value, nil
		}
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
	actions, exists := state.contract.events[tag]
	if !exists {
		return state.issue(state.contract.discriminator.path, "undeclared stream event tag", nil)
	}
	sequence := state.accepted.sequence
	hasAction := false
	var responseID, responsePath string
	hasResponseIdentity := false
	for _, action := range actions {
		if action.when != nil {
			condition, err := action.when.evaluate(conditions)
			if err != nil {
				return state.issue(state.contract.discriminator.path, "stream action condition failed", err)
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return state.issue(state.contract.discriminator.path, "stream action condition must produce a boolean", err)
			}
			if !isApplicable {
				continue
			}
		}
		if err := state.checkActionPosition(action); err != nil {
			return err
		}
		if action.isControl {
			value, err := read(action.responseID)
			if err != nil {
				return err
			}
			if !value.IsZero() {
				if _, err := state.readResponseIdentity(action.responseID, read); err != nil {
					return err
				}
			}
		} else if !hasResponseIdentity || action.responseID.path != responsePath {
			responseID, err = state.readResponseIdentity(action.responseID, read)
			if err != nil {
				return err
			}
			responsePath = action.responseID.path
			hasResponseIdentity = true
		}
		if !hasAction && state.contract.sequence.path != "" {
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
		if err := state.consumeAction(ctx, action, responseID, read); err != nil {
			return err
		}
		hasAction = true
	}
	if !hasAction {
		return state.issue(state.contract.discriminator.path, "stream event does not match a declared action", nil)
	}
	state.accepted.sequence, state.accepted.hasSequence = sequence, state.contract.sequence.path != ""
	state.hasPendingDelivery = state.direction == EncodeEvent
	return nil
}

func (state *packageStreamValidation) checkActionPosition(action compiledPackageStreamEvent) error {
	if action.isControl {
		return nil
	}
	if state.replay.isTerminal && action.action != UsageUpdated {
		return state.issue(state.contract.discriminator.path, "event arrived after terminal; only usage tails are permitted", nil)
	}
	if action.action == ResponseStarted && state.accepted.hasStarted {
		return state.issue(state.contract.discriminator.path, "response started more than once", nil)
	}
	if action.action != ResponseStarted && !state.accepted.hasStarted {
		return state.issue(state.contract.discriminator.path, "event requires a preceding response start", nil)
	}
	return nil
}

func (state *packageStreamValidation) consumeAction(ctx context.Context, definition compiledPackageStreamEvent, responseID string, read func(streamField) (Value, error)) error {
	if definition.isControl {
		return ctx.Err()
	}
	event := Event{SchemaVersion: state.target.coreSchemaVersion(), Source: state.target.Protocol, Type: definition.action}
	identityBytes := state.identityBytes
	if state.accepted.responseID == "" {
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
	if state.accepted.responseID == "" {
		state.accepted.responseID = responseID
	}
	state.accepted.hasStarted = true
	state.identityBytes = identityBytes
	switch definition.action {
	case ItemStarted:
		state.openItems++
	case ItemFinished:
		state.openItems--
	case ResponseFinished, OperationFailed, OperationCancelled:
		state.accepted.isTerminal = true
	}
	if state.contract.hasTools && (event.Type == OperationFailed || event.Type == OperationCancelled) {
		for _, tool := range state.replay.state.tools {
			tool.input.Reset()
		}
		state.replay.state.buffered = 0
	}
	return nil
}

func (state *packageStreamValidation) readPayload(event *Event, definition *compiledPackageStreamEvent, read func(streamField) (Value, error)) (int, error) {
	switch event.Type {
	case ItemStarted, ItemDelta, ItemFinished:
		itemID, err := state.readItemIdentity(definition.itemID, read)
		if err != nil {
			return 0, err
		}
		event.ItemID = StringValue(itemID)
		// Generic finishes close the established item, as on transports whose
		// end marker carries no kind. Tool finishes still promise a tool item.
		if (state.contract.hasTools || state.contract.hasTypedItems) && (event.Type == ItemDelta || event.Type == ItemFinished && definition.isTool) {
			key := state.replay.identities.aliases["item:"+itemID]
			if item := state.replay.items[key]; item != nil && ((item.kind == ToolCallNode) != definition.isTool || event.Type == ItemDelta && !definition.isTool && item.kind != definition.kind) {
				return 0, state.issue(definition.itemID.path, "stream action does not match the established item kind", nil)
			}
		}
		if event.Type == ItemStarted {
			if definition.isTool {
				name, err := state.readIdentity(definition.tool.name, read)
				if err != nil {
					return 0, err
				}
				callID, err := state.readIdentity(definition.tool.callID, read)
				if err != nil {
					return 0, err
				}
				event.Item = &Node{Kind: ToolCallNode, Name: StringValue(name), CallID: StringValue(callID), Input: &ToolInput{Kind: JSONInput}}
				return len(itemID) + len(name) + len(callID), nil
			}
			event.Item = &Node{Kind: definition.kind}
			return len(itemID), nil
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
	case UsageUpdated:
		var err error
		event.Usage, err = state.readUsage(definition.usage[:], read)
		return 0, err
	case ResponseFinished:
		if state.openItems != 0 {
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
		case "/delta":
			issue.Path = event.delta.path
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
			switch event.action {
			case ItemStarted:
				issue.Path = event.tool.callID.path
			case ItemDelta:
				issue.Path = event.delta.path
			case ItemFinished:
				issue.Path = event.itemID.path
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
