package protocol

import "fmt"

type replayItem struct {
	kind          NodeKind
	input         InputKind
	callID        string
	name          string
	isFinished    bool
	summaryParts  int
	visibleParts  int
	reasoningForm ReasoningForm
}

// EventReplay is the shared semantic event validator for fixtures and runtime
// sessions. It retains bounded identities/digests, not already-forwarded text.
type EventReplay struct {
	state      *StreamState
	sequence   *SequenceTracker
	items      map[string]*replayItem
	identities *ItemIdentities
	limits     Limits
	target     Target
	usage      *Usage
	isTerminal bool
	terminal   EventType
}

// NewEventReplay creates isolated state for one response event sequence.
func NewEventReplay(target Target, limits Limits) (*EventReplay, error) {
	state, err := NewStreamState(limits)
	if err != nil {
		return nil, err
	}
	return &EventReplay{state: state, sequence: NewSequenceTracker(limits), items: map[string]*replayItem{}, identities: NewItemIdentities(limits.StateItems), limits: limits, target: target}, nil
}

// Consume validates one semantic event. False means an identical, explicitly
// sequenced event was already observed; callers must not forward it twice.
func (replay *EventReplay) Consume(event Event) (bool, error) {
	if err := IssuesError(CheckEvent(event, replay.target, replay.limits)); err != nil {
		return false, err
	}
	if accepted, err := acceptEventSequence(replay.sequence, event); err != nil || !accepted {
		return accepted, err
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
		replay.terminal = ResponseFinished
	case OperationFailed, OperationCancelled:
		// Failed/cancelled operations may legitimately leave partial arguments.
		replay.isTerminal = true
		replay.terminal = event.Type
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
		item = &replayItem{kind: event.Item.Kind, reasoningForm: event.Item.ReasoningForm}
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
	if event.Item != nil && event.Item.ReasoningForm != item.reasoningForm {
		return streamIssue(InvalidAssociation, "/item/reasoningForm", "reasoning representation changed")
	}
	if item.kind == ToolCallNode {
		return replay.consumeTool(key, item, event)
	}
	if event.Item != nil && (event.Item.ReasoningForm == SummaryReasoning || event.Item.ReasoningForm == StructuredReasoning) {
		if len(event.Item.Children) < item.summaryParts {
			return streamIssue(UpstreamContractViolation, "/item/children", "reasoning summary removed an emitted part")
		}
		for index, child := range event.Item.Children {
			if child.Kind != TextNode {
				return streamIssue(UnsupportedCapability, "/item/children", "summary parts require text")
			}
			text, err := readString(child.Payload)
			if err != nil {
				return err
			}
			if _, err := replay.state.TrackText(fmt.Sprintf("%s/summary/%d", key, index), text, true); err != nil {
				return err
			}
		}
		item.summaryParts = len(event.Item.Children)
		if len(event.Item.ReasoningContent) < item.visibleParts {
			return streamIssue(UpstreamContractViolation, "/item/reasoningContent", "reasoning content removed an emitted part")
		}
		for index, part := range event.Item.ReasoningContent {
			text, err := readString(part.Payload)
			if err != nil {
				return err
			}
			if _, err := replay.state.TrackText(fmt.Sprintf("%s/reasoning-content/%d", key, index), text, true); err != nil {
				return err
			}
		}
		item.visibleParts = len(event.Item.ReasoningContent)
	}
	if item.reasoningForm != "" && !event.Delta.IsZero() {
		return streamIssue(InvalidAssociation, "/delta", "structured reasoning updates require an indexed part snapshot")
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
	return replay.identities.Resolve(event)
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
