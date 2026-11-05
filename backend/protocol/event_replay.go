package protocol

import "fmt"

type replayItem struct {
	openChildren       int
	parent             string
	collection         string
	hasSummaryChildren bool
	hasContentChildren bool
	wireID             Value
	role               Value
	kind               NodeKind
	input              InputKind
	callID             string
	name               string
	isFinished         bool
	summaryParts       int
	visibleParts       int
	reasoningForm      ReasoningForm
}

// EventReplay is the shared semantic event validator for fixtures and runtime
// sessions. It retains bounded identities/digests, not already-forwarded text.
type EventReplay struct {
	retention     streamReservation
	state         *StreamState
	sequence      *SequenceTracker
	items         map[string]*replayItem
	identities    *ItemIdentities
	limits        Limits
	embeddedItems int
	identityBytes int
	usageBytes    int
	target        Target
	usage         *Usage
	isTerminal    bool
	isReleased    bool
	terminal      EventType
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
func (replay *EventReplay) Consume(event Event) (accepted bool, err error) {
	if replay.isReleased {
		return false, streamIssue(InvalidAssociation, "/state", "released event replay cannot resume")
	}
	defer func() {
		if err == nil && accepted {
			err = replay.checkRetainedBytes()
			accepted = err == nil
		}
	}()
	if err := IssuesError(CheckEvent(event, replay.target, replay.limits)); err != nil {
		return false, err
	}
	if accepted, err := acceptEventSequence(replay.sequence, event); err != nil || !accepted {
		return accepted, err
	}
	if replay.isTerminal && event.Type != UsageUpdated {
		return false, streamIssue(UpstreamContractViolation, "/type", "event arrived after terminal; only usage tails are permitted")
	}
	if event.Usage != nil {
		if err := replay.mergeUsage(event.Usage); err != nil {
			return false, err
		}
	}
	if event.Response != nil && event.Response.Usage != nil {
		if err := replay.mergeUsage(event.Response.Usage); err != nil {
			return false, err
		}
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
		replay.releaseToolInputs()
		replay.isTerminal = true
		replay.terminal = event.Type
	}
	return true, nil
}

func (replay *EventReplay) releaseToolInputs() {
	for _, tool := range replay.state.tools {
		tool.input.Reset()
	}
	replay.state.buffered = 0
}

func (replay *EventReplay) consumeItem(event Event) error {
	key, err := replay.itemKey(event)
	if err != nil {
		return err
	}
	parent, err := replay.identities.Parent(event.ParentID)
	if err != nil {
		return err
	}
	if parent != "" {
		if err := replay.checkParent(replay.items[parent], event); err != nil {
			return err
		}
		if event.Collection == "" && event.Item != nil && !isMessageStreamChild(*event.Item) {
			return streamIssue(UnsupportedCapability, "/item", "message stream children require text, refusal, unstructured reasoning or client tool leaves")
		}
	}
	item := replay.items[key]
	if event.Type == ItemStarted {
		if item != nil {
			return streamIssue(InvalidAssociation, "/itemId", "item started more than once")
		}
		if event.Item == nil {
			return streamIssue(InvalidInput, "/item", "item start requires semantic item kind")
		}
		if err := replay.reserveItems(1); err != nil {
			return err
		}
		item = &replayItem{kind: event.Item.Kind, reasoningForm: event.Item.ReasoningForm, parent: parent, collection: event.Collection}
		if event.Item.Input != nil {
			item.input = event.Item.Input.Kind
		}
		replay.items[key] = item
		if parent != "" {
			p := replay.items[parent]
			p.openChildren++
			p.hasSummaryChildren = p.hasSummaryChildren || event.Collection == "summary"
			p.hasContentChildren = p.hasContentChildren || event.Collection == "reasoning_content"
		}
	}
	if item == nil {
		return streamIssue(InvalidAssociation, "/itemId", "delta or completion has no preceding item start")
	}
	previousBytes := item.retainedBytes()
	defer func() { replay.identityBytes += item.retainedBytes() - previousBytes }()
	if item.parent != parent {
		return streamIssue(InvalidAssociation, "/parentId", "item parent changed")
	}
	if item.collection != event.Collection {
		return streamIssue(InvalidAssociation, "/collection", "item collection changed")
	}
	if event.Type == ItemFinished && item.openChildren != 0 {
		return streamIssue(InvalidAssociation, "/parentId", "parent finished before its content")
	}
	if item.kind == MessageNode {
		if event.Item != nil {
			for _, entry := range []struct {
				value   Value
				current *Value
				path    string
			}{{event.Item.ID, &item.wireID, "/item/id"}, {event.Item.Role, &item.role, "/item/role"}} {
				if entry.value.IsZero() {
					continue
				}
				if !entry.current.IsZero() && *entry.current != entry.value {
					return streamIssue(InvalidAssociation, entry.path, "message identity or role changed")
				}
				*entry.current = entry.value
			}
		}
		if event.Item != nil && len(event.Item.Children) > 0 {
			return streamIssue(InvalidAssociation, "/item/children", "streamed message children require their own associated events")
		}
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
	if err := replay.consumeItemContent(key, item, event); err != nil {
		return err
	}
	if event.Type == ItemFinished {
		item.isFinished = true
		if parent != "" {
			replay.items[parent].openChildren--
		}
	}
	return nil
}

func isMessageStreamChild(node Node) bool {
	if node.ReasoningForm != "" || len(node.Children) != 0 || node.ReasoningContent != nil {
		return false
	}
	switch node.Kind {
	case TextNode, RefusalNode, ReasoningNode, ToolCallNode:
		return true
	default:
		return false
	}
}

func (replay *EventReplay) consumeItemContent(key string, item *replayItem, event Event) error {
	if item.kind == ToolCallNode {
		return replay.consumeTool(key, item, event)
	}
	if item.kind == ServerToolNode {
		return replay.consumeServerTool(key, item, event)
	}
	if event.Item != nil && item.reasoningForm != "" {
		if err := replay.trackReasoningSnapshot(key, item, event.Item); err != nil {
			return err
		}
	}
	if item.reasoningForm != "" && !event.Delta.IsZero() {
		return streamIssue(InvalidAssociation, "/delta", "structured reasoning text requires associated child events")
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
	return nil
}

func (replay *EventReplay) checkParent(parent *replayItem, event Event) error {
	if parent == nil || parent.isFinished || parent.parent != "" {
		return streamIssue(InvalidAssociation, "/parentId", "content requires an open top-level parent")
	}
	if event.Collection == "" {
		if parent.kind != MessageNode {
			return streamIssue(InvalidAssociation, "/collection", "reasoning children require a collection")
		}
		return nil
	}
	if parent.kind != ReasoningNode || (parent.reasoningForm != SummaryReasoning && parent.reasoningForm != StructuredReasoning) {
		return streamIssue(InvalidAssociation, "/parentId", "collection requires a structured reasoning parent")
	}
	if event.Collection == "reasoning_content" && parent.reasoningForm != StructuredReasoning {
		return streamIssue(InvalidAssociation, "/collection", "visible content requires structured reasoning")
	}
	if event.Type == ItemStarted && ((event.Collection == "summary" && parent.summaryParts != 0) || (event.Collection == "reasoning_content" && parent.visibleParts != 0)) {
		return streamIssue(InvalidAssociation, "/collection", "embedded snapshots and associated children cannot share a collection")
	}
	return nil
}

func (replay *EventReplay) trackReasoningSnapshot(key string, item *replayItem, node *Node) error {
	for _, part := range []struct {
		name        string
		path        string
		nodes       []Node
		count       *int
		hasChildren bool
	}{
		{"summary", "/item/children", node.Children, &item.summaryParts, item.hasSummaryChildren},
		{"reasoning-content", "/item/reasoningContent", node.ReasoningContent, &item.visibleParts, item.hasContentChildren},
	} {
		if part.hasChildren {
			if len(part.nodes) != 0 {
				return streamIssue(InvalidAssociation, part.path, "associated children cannot be replaced by an embedded snapshot")
			}
			continue
		}
		if part.nodes == nil {
			continue // Metadata updates do not replace a text collection.
		}
		if len(part.nodes) < *part.count {
			return streamIssue(UpstreamContractViolation, part.path, "reasoning snapshot removed an emitted part")
		}
		additional := len(part.nodes) - *part.count
		if err := replay.reserveItems(additional); err != nil {
			return err
		}
		for index, child := range part.nodes {
			text, err := readString(child.Payload)
			if err != nil {
				return err
			}
			trackerKey := fmt.Sprintf("%s/%s/%d", key, part.name, index)
			if _, err := replay.state.TrackText(trackerKey, text, true); err != nil {
				return err
			}
			if index >= *part.count {
				replay.identityBytes += len(trackerKey)
			}
		}
		replay.embeddedItems += additional
		*part.count = len(part.nodes)
	}
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
	return nil
}

func (replay *EventReplay) itemKey(event Event) (string, error) {
	return replay.identities.Resolve(event)
}

func (item *replayItem) retainedBytes() int {
	return item.wireID.Len() + item.role.Len() + len(item.callID) + len(item.name)
}

func (replay *EventReplay) retainedItems() int {
	items := len(replay.items) + replay.embeddedItems
	if replay.usage != nil {
		items += len(replay.usage.Details)
	}
	return items
}

func (replay *EventReplay) mergeUsage(update *Usage) error {
	replay.usage = MergeUsage(replay.usage, update)
	if err := replay.reserveItems(0); err != nil {
		return err
	}
	size, err := measureSemanticValue(replay.usage, replay.limits)
	if err != nil {
		return err
	}
	replay.usageBytes = size.bytes
	return nil
}

func (replay *EventReplay) reserveItems(additional int) error {
	items := replay.retainedItems() + additional
	if items > replay.limits.StateItems {
		return streamIssue(LimitExceeded, "/item", "stream identities and embedded content exceed the item limit")
	}
	// The latest sequence is a fixed lane fact. Older deduplication records
	// use only spare capacity and may expire as semantic state grows.
	replay.sequence.trim(max(1, replay.limits.StateItems-items+1))
	return nil
}

func (replay *EventReplay) retainedBytes() int {
	return replay.identities.bytes + replay.identityBytes + replay.state.buffered + replay.usageBytes
}

func (replay *EventReplay) checkRetainedBytes() error {
	bytes := replay.retainedBytes()
	if bytes > replay.limits.BufferBytes {
		return streamIssue(LimitExceeded, "/item", "stream identities and buffered input exceed the buffer limit")
	}
	if replay.retention.budget != nil {
		items := replay.retainedItems()
		if err := replay.retention.check(items, bytes); err != nil {
			return err
		}
		replay.retention.commit(items, bytes)
	}
	return nil
}

func (replay *EventReplay) completeTools() error {
	for key, item := range replay.items {
		if (item.kind == MessageNode || item.kind == ServerToolNode || item.reasoningForm != "") && !item.isFinished {
			return streamIssue(UpstreamContractViolation, "/itemId", "response finished before its container")
		}
		if item.kind == ToolCallNode && (item.callID == "" || item.name == "") {
			return streamIssue(InvalidAssociation, key, "completed tool requires its name and call ID")
		}
	}
	return nil
}

// Finish requires a declared terminal; network EOF is not successful completion.
func (replay *EventReplay) Finish() error {
	if replay.isReleased {
		return streamIssue(InvalidAssociation, "/state", "released event replay cannot finish")
	}
	if !replay.isTerminal {
		return streamIssue(UpstreamContractViolation, "/type", "event sequence ended without a terminal")
	}
	if budget := replay.retention.budget; budget != nil {
		return budget.Check()
	}
	return nil
}

// Usage returns a copy of the merged counters, including usage after terminal.
func (replay *EventReplay) Usage() *Usage { return MergeUsage(nil, replay.usage) }

// Release drops request-owned identities, history and input buffers. A released
// replay cannot accept more events or establish successful completion.
func (replay *EventReplay) Release() {
	replay.releaseRetention()
	*replay = EventReplay{isReleased: true}
}
