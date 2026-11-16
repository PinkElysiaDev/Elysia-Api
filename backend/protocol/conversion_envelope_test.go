package protocol

import (
	"strconv"
	"testing"
)

func TestDeliveryFrameUsageIsolation(t *testing.T) {
	count := func(n int64) *Usage { return &Usage{Input: &Counter{Count: n, Origin: ObservedCount}} }
	for _, fixture := range []struct{ eventVersion, responseVersion int }{
		{0, 0},
		{SemanticSchemaVersion, SemanticSchemaVersion},
		{ProtocolCoreVersion, ProtocolCoreVersion},
		{ProtocolCoreVersion + 1, ProtocolCoreVersion + 1},
	} {
		t.Run(strconv.Itoa(fixture.eventVersion), func(t *testing.T) {
			events := []Event{
				{SchemaVersion: fixture.eventVersion, Type: ResponseStarted, ResponseID: StringValue("a")},
				{SchemaVersion: fixture.eventVersion, Type: ResponseStarted, ResponseID: StringValue("b")},
				{Type: UsageUpdated, ResponseID: StringValue("a"), Usage: count(3)},
				{Type: UsageUpdated, ResponseID: StringValue("b"), Usage: count(7)},
				{Type: UsageUpdated, Usage: count(99)}, // Ambiguous: do not attach to either response.
			}
			projected := DeliveryFrameEvents(events)
			if projected[0].Response.Usage.Input.Count != 3 || projected[1].Response.Usage.Input.Count != 7 || events[0].Response != nil {
				t.Fatal("same-frame usage crossed response identities or mutated source events", projected)
			}
			for _, event := range projected[:2] {
				if event.SchemaVersion != fixture.eventVersion || event.Response.SchemaVersion != fixture.responseVersion {
					t.Fatal("response version did not follow its start event", event)
				}
			}
		})
	}
}

func TestEventFailureDiscardsWaitingItems(t *testing.T) {
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "wait-test", Rules: []ConversionRule{{ID: "wait-tool", Enabled: true, Phase: ConversionEvent, Action: "buffer_node", Match: ConversionMatch{NodeKind: ToolCallNode}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []EventType{OperationFailed, OperationCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			state := NewConversionEventState(conversion, ConversionContext{})
			start := toolStart()
			for _, event := range []Event{start, {SchemaVersion: start.SchemaVersion, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue("{")}} {
				if output, err := state.PushContext(t.Context(), event); err != nil || len(output) != 0 {
					t.Fatal("incomplete tool escaped waiting", output, err)
				}
			}
			failure := Event{SchemaVersion: start.SchemaVersion, Type: terminal, Error: StringValue("upstream stopped")}
			output, err := state.PushContext(t.Context(), failure)
			if err != nil || len(output) != 1 || output[0].Type != terminal || output[0].Error != failure.Error {
				t.Fatal("waiting replaced the original failure", output, err)
			}
			if len(state.pending) != 0 || len(state.open) != 0 || state.bytes != 0 {
				t.Fatal("failed stream retained waiting content")
			}
			if tail, err := state.Drain(); err != nil || len(tail) != 0 {
				t.Fatal("failed stream produced a second terminal", tail, err)
			}
		})
	}
}

func TestEventReleaseDiscardsUnfinishedInput(t *testing.T) {
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "release-test", Rules: []ConversionRule{{ID: "wait-tool", Enabled: true, Phase: ConversionEvent, Action: "buffer_node", Match: ConversionMatch{NodeKind: ToolCallNode}}}})
	if err != nil {
		t.Fatal(err)
	}
	state := NewConversionEventState(conversion, ConversionContext{})
	start := toolStart()
	for _, event := range []Event{start, {SchemaVersion: start.SchemaVersion, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue(`{"partial":`)}} {
		if output, err := state.PushContext(t.Context(), event); err != nil || len(output) != 0 {
			t.Fatal("incomplete tool escaped waiting", output, err)
		}
	}
	state.Release()
	if state.pending != nil || state.open != nil || state.bytes != 0 {
		t.Fatal("release retained waiting content")
	}
	if output, err := state.Drain(); err == nil || len(output) != 0 {
		t.Fatal("release published unfinished input", output, err)
	}
	if _, err := state.PushContext(t.Context(), start); err == nil {
		t.Fatal("released conversion resumed")
	}
}
