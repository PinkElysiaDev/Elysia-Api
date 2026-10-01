package protocol

import "strconv"

const itemIdentityKinds = 3

type replayItem struct {
	kind       NodeKind
	input      InputKind
	callID     string
	name       string
	isFinished bool
}

// EventReplay is the shared semantic event validator for fixtures and runtime
// sessions. It retains bounded identities/digests, not already-forwarded text.
type EventReplay struct {
	state      *StreamState
	sequence   *SequenceTracker
	items      map[string]*replayItem
	aliases    map[string]string
	limits     Limits
	target     Target
	usage      *Usage
	isTerminal bool
}

// NewEventReplay creates isolated state for one response event sequence.
func NewEventReplay(target Target, limits Limits) (*EventReplay, error) {
	state, err := NewStreamState(limits)
	if err != nil {
		return nil, err
	}
	return &EventReplay{state: state, sequence: NewSequenceTracker(limits), items: map[string]*replayItem{}, aliases: map[string]string{}, limits: limits, target: target}, nil
}

// Consume validates one semantic event. False means an identical, explicitly
// sequenced event was already observed; callers must not forward it twice.
func (replay *EventReplay) Consume(event Event) (bool, error) {
	if err := IssuesError(CheckEvent(event, replay.target, replay.limits)); err != nil {
		return false, err
	}
	if !event.Sequence.IsZero() {
		var sequence int64
		if err := event.Sequence.Decode(&sequence); err != nil {
			return false, streamIssue(InvalidInput, "/sequence", "event sequence must be an integer")
		}
		value, err := EncodeValue(event)
		if err != nil {
			return false, err
		}
		var canonical any
		if err := value.Decode(&canonical); err != nil {
			return false, err
		}
		value, err = EncodeValue(canonical)
		if err != nil {
			return false, err
		}
		accepted, err := replay.sequence.Accept(sequence, value.Bytes())
		if err != nil || !accepted {
			return accepted, err
		}
	}
	if replay.isTerminal && event.Type != UsageUpdated {
		return false, streamIssue(UpstreamContractViolation, "/type", "event arrived after terminal; only usage tails are permitted")
	}
	replay.usage = MergeUsage(replay.usage, event.Usage)
	if event.Response != nil {
		replay.usage = MergeUsage(replay.usage, event.Response.Usage)
	}
	switch event.Type {
	case ItemStarted, ItemDelta, ItemSnapshot, ItemFinished:
		return true, replay.consumeItem(event)
	case ResponseFinished:
		if err := replay.completeTools(); err != nil {
			return false, err
		}
		if err := replay.state.Finish(); err != nil {
			return false, err
		}
		replay.isTerminal = true
	case OperationFailed, OperationCancelled:
		// Failed/cancelled operations may legitimately leave partial arguments.
		replay.isTerminal = true
	}
	return true, nil
}

func (replay *EventReplay) consumeItem(event Event) error {
	key, err := replay.itemKey(event)
	if err != nil {
		return err
	}
	item := replay.items[key]
	if event.Type == ItemStarted {
		if item != nil {
			return streamIssue(InvalidAssociation, "/itemId", "item started more than once")
		}
		if event.Item == nil {
			return streamIssue(InvalidInput, "/item", "item start requires semantic item kind")
		}
		if len(replay.items) >= replay.limits.StateItems {
			return streamIssue(LimitExceeded, "/item", "too many stream items")
		}
		item = &replayItem{kind: event.Item.Kind}
		if event.Item.Input != nil {
			item.input = event.Item.Input.Kind
		}
		replay.items[key] = item
	}
	if item == nil {
		return streamIssue(InvalidAssociation, "/itemId", "delta or completion has no preceding item start")
	}
	if item.isFinished {
		return streamIssue(UpstreamContractViolation, "/itemId", "content arrived after item completion")
	}
	if event.Item != nil && event.Item.Kind != item.kind {
		return streamIssue(InvalidAssociation, "/item/kind", "item kind changed")
	}
	if item.kind == ToolCallNode {
		return replay.consumeTool(key, item, event)
	}
	value := event.Delta
	isSnapshot := event.Type == ItemSnapshot || event.Type == ItemFinished || event.Type == ItemStarted
	if isSnapshot && event.Item != nil {
		value = event.Item.Payload
	}
	if !value.IsZero() {
		if item.kind != TextNode && item.kind != ReasoningNode && item.kind != RefusalNode {
			return streamIssue(UnsupportedCapability, "/item", "media/native item accumulation requires its declared transport mechanism")
		}
		text, err := readString(value)
		if err != nil {
			return err
		}
		if _, err := replay.state.TrackText(key, text, isSnapshot); err != nil {
			return err
		}
	}
	item.isFinished = event.Type == ItemFinished
	return nil
}

func (replay *EventReplay) consumeTool(key string, item *replayItem, event Event) error {
	value := event.Delta
	isSnapshot := event.Type == ItemSnapshot || event.Type == ItemFinished || event.Type == ItemStarted
	if event.Item != nil {
		if !event.Item.Name.IsZero() {
			name, err := readString(event.Item.Name)
			if err != nil {
				return err
			}
			if item.name != "" && item.name != name {
				return streamIssue(InvalidAssociation, "/item/name", "tool name changed")
			}
			item.name = name
		}
		if !event.Item.CallID.IsZero() {
			callID, err := readString(event.Item.CallID)
			if err != nil {
				return err
			}
			if item.callID != "" && item.callID != callID {
				return streamIssue(InvalidAssociation, "/item/callId", "tool call ID changed")
			}
			item.callID = callID
		}
		if event.Item.Input != nil {
			if event.Item.Input.Kind != item.input {
				return streamIssue(InvalidAssociation, "/item/input/kind", "tool input kind changed")
			}
			if isSnapshot {
				value = event.Item.Input.Value
			}
		}
	}
	if !event.CallID.IsZero() {
		id, err := readString(event.CallID)
		if err != nil {
			return err
		}
		if item.callID != "" && item.callID != id {
			return streamIssue(InvalidAssociation, "/callId", "tool call identity changed")
		}
		item.callID = id
	}
	if err := replay.state.ObserveTool(key, item.callID, item.input); err != nil {
		return err
	}
	if !value.IsZero() {
		var text string
		if isSnapshot && item.input == JSONInput {
			text = string(value.Bytes())
		} else {
			var err error
			text, err = readString(value)
			if err != nil {
				return err
			}
		}
		if err := replay.state.AppendTool(key, text, isSnapshot, event.Type == ItemFinished); err != nil {
			return err
		}
	} else if event.Type == ItemFinished {
		if err := replay.state.AppendTool(key, "", false, true); err != nil {
			return err
		}
	}
	item.isFinished = event.Type == ItemFinished
	return nil
}

func (replay *EventReplay) itemKey(event Event) (string, error) {
	var identities []string
	for _, entry := range []struct {
		prefix string
		value  Value
	}{{"item:", event.ItemID}, {"call:", event.CallID}} {
		if !entry.value.IsZero() {
			id, err := readString(entry.value)
			if err != nil || id == "" {
				return "", streamIssue(InvalidAssociation, "/itemId", "item/call identity must be a nonempty string")
			}
			identities = append(identities, entry.prefix+id)
		}
	}
	if event.Index != nil {
		identities = append(identities, "index:"+strconv.Itoa(*event.Index))
	}
	if len(identities) == 0 {
		return "", streamIssue(InvalidAssociation, "/itemId", "item has no identity")
	}
	key := ""
	for _, id := range identities {
		if previous := replay.aliases[id]; previous != "" {
			if key != "" && key != previous {
				return "", streamIssue(InvalidAssociation, "/itemId", "event links two distinct items")
			}
			key = previous
		}
	}
	if key == "" {
		key = identities[0]
	}
	newAliases := 0
	for _, id := range identities {
		if replay.aliases[id] == "" {
			newAliases++
		}
	}
	if len(replay.aliases)+newAliases > replay.limits.StateItems*itemIdentityKinds {
		return "", streamIssue(LimitExceeded, "/itemId", "too many item aliases")
	}
	for _, id := range identities {
		replay.aliases[id] = key
	}
	return key, nil
}

func (replay *EventReplay) completeTools() error {
	for key, item := range replay.items {
		if item.kind == ToolCallNode && (item.callID == "" || item.name == "") {
			return streamIssue(InvalidAssociation, key, "completed tool requires its name and call ID")
		}
	}
	return nil
}

// Finish requires a declared terminal; network EOF is not successful completion.
func (replay *EventReplay) Finish() error {
	if !replay.isTerminal {
		return streamIssue(UpstreamContractViolation, "/type", "event sequence ended without a terminal")
	}
	return nil
}

// Usage returns a copy of the merged counters, including usage after terminal.
func (replay *EventReplay) Usage() *Usage { return MergeUsage(nil, replay.usage) }
