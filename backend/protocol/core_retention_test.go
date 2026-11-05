package protocol

import (
	"strings"
	"testing"
)

func TestCoreRetentionCombinesContainersAndEmbeddedReasoning(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	replay, err := NewEventReplay(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	node := &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning,
		Children:         []Node{{Kind: TextNode, Payload: StringValue("summary")}},
		ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("thought")}}}
	start := reasoningCollectionEvent(ItemStarted, "r", "", "", node)
	if _, err := replay.Consume(start); err != nil {
		t.Fatal(err)
	}
	start.Type = ItemSnapshot
	if _, err := replay.Consume(start); err != nil || replay.retainedItems() != 3 {
		t.Fatal("repeated snapshot charged another item", replay.retainedItems(), err)
	}
	if _, err := replay.Consume(reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode})); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("container and embedded tracker obtained independent capacities", err)
	}
}

func TestCoreRetentionCombinesIdentitiesAndToolInput(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 512
	replay, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		start := toolStart()
		start.ItemID = StringValue(id + strings.Repeat("i", 170))
		start.Item.CallID = StringValue(id)
		if _, err := replay.Consume(start); err != nil {
			t.Fatal(err)
		}
	}
	event := Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("a" + strings.Repeat("i", 170)), Delta: StringValue(strings.Repeat(" ", 180))}
	if _, err := replay.Consume(event); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("identities and input buffers obtained independent byte budgets", err)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: OperationCancelled}); err != nil {
		t.Fatal(err)
	}
	if replay.state.buffered != 0 {
		t.Fatal("cancel retained tool input")
	}
}

func TestCoreReleaseClosesReplay(t *testing.T) {
	replay, err := NewEventReplay(replayTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	start := toolStart()
	start.Sequence = coreInteger(1)
	if _, err := replay.Consume(start); err != nil {
		t.Fatal(err)
	}
	replay.Release()
	if replay.state != nil || replay.sequence != nil || replay.items != nil || replay.identities != nil {
		t.Fatal("release retained replay state")
	}
	if isAccepted, err := replay.Consume(Event{SchemaVersion: 1, Type: OperationCancelled}); err == nil || isAccepted {
		t.Fatal("released replay resumed", err)
	}
	if err := replay.Finish(); err == nil {
		t.Fatal("released replay acquired successful completion")
	}
}

func TestCoreSequenceWindowYieldsCapacityToSemanticItems(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	replay, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"a", "b", "c"} {
		event := messageEvent(ItemStarted, id, "", &Node{Kind: TextNode})
		event.Sequence = coreInteger(int64(index))
		if _, err := replay.Consume(event); err != nil {
			t.Fatal(err)
		}
		if replay.retainedItems()+replay.sequence.count-1 > limits.StateItems {
			t.Fatal("sequence history escaped the combined item budget")
		}
		if accepted, err := replay.Consume(event); err != nil || accepted {
			t.Fatal("latest repeated frame was not deduplicated", accepted, err)
		}
	}
	if replay.sequence.count != 1 {
		t.Fatal("retained history when all dynamic capacity belongs to items")
	}
}

func TestSequenceWindowShrinksAfterWrap(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 4
	tracker := NewSequenceTracker(limits)
	for sequence := int64(1); sequence <= 7; sequence++ {
		if _, err := tracker.Accept(sequence, []byte("same")); err != nil {
			t.Fatal(err)
		}
	}
	tracker.trim(2)
	for _, sequence := range []int64{6, 7} {
		if accepted, err := tracker.Accept(sequence, []byte("same")); err != nil || accepted {
			t.Fatal("trim lost recent replay evidence", sequence, accepted, err)
		}
	}
	if _, err := tracker.Accept(5, []byte("same")); err == nil {
		t.Fatal("trim retained expired history")
	}
	if _, err := tracker.Accept(8, []byte("same")); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Accept(6, []byte("same")); err == nil {
		t.Fatal("trim did not resume eviction from the oldest record")
	}
}

func TestCoreUsageDetailsShareDynamicCapacity(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 2
	replay, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Consume(messageEvent(ItemStarted, "text", "", &Node{Kind: TextNode})); err != nil {
		t.Fatal(err)
	}
	update := Event{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{Details: map[string]Counter{"search": {Count: 1, Origin: ObservedCount}}}}
	for range 2 {
		if _, err := replay.Consume(update); err != nil {
			t.Fatal("repeated usage counter consumed a new item", err)
		}
	}
	update.Usage = &Usage{Details: map[string]Counter{"execution": {Count: 1, Origin: ObservedCount}}}
	if _, err := replay.Consume(update); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("usage details escaped the semantic item budget", err)
	}
}

func TestSessionSequenceHistorySharesResponseCapacity(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 2
	session := testSession(t, SessionPolicy{}, limits)
	create := sessionEvent(ResponseCreate, "")
	create.Sequence = coreInteger(1)
	consumeSession(t, session, ClientEvent, create)
	start := sessionEvent(ResponseStarted, "response")
	start.Sequence = coreInteger(1)
	consumeSession(t, session, UpstreamEvent, start)
	item := toolStart()
	item.ResponseID, item.Sequence = start.ResponseID, coreInteger(2)
	consumeSession(t, session, UpstreamEvent, item)
	item.Type, item.Sequence = ItemFinished, coreInteger(3)
	item.Item.Input.Value = fixtureValue(t, map[string]any{"ok": true})
	consumeSession(t, session, UpstreamEvent, item)
	result := sessionEvent(ToolResultSubmitted, "")
	result.Sequence = coreInteger(2)
	result.Item = &Node{Kind: ToolResultNode, CallID: item.Item.CallID, Payload: StringValue("done")}
	consumeSession(t, session, ClientEvent, result)
	if session.clientSequence.count != 1 || session.serverSequence.count != 1 {
		t.Fatal("lane history retained a separate dynamic budget")
	}
}
