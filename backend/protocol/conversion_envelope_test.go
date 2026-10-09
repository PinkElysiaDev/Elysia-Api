package protocol

import "testing"

func TestDeliveryFrameUsageIsolation(t *testing.T) {
	count := func(n int64) *Usage { return &Usage{Input: &Counter{Count: n, Origin: ObservedCount}} }
	events := []Event{
		{Type: ResponseStarted, ResponseID: StringValue("a")},
		{Type: ResponseStarted, ResponseID: StringValue("b")},
		{Type: UsageUpdated, ResponseID: StringValue("a"), Usage: count(3)},
		{Type: UsageUpdated, ResponseID: StringValue("b"), Usage: count(7)},
		{Type: UsageUpdated, Usage: count(99)}, // Ambiguous: do not attach to either response.
	}
	projected := DeliveryFrameEvents(events)
	if projected[0].Response.Usage.Input.Count != 3 || projected[1].Response.Usage.Input.Count != 7 || events[0].Response != nil {
		t.Fatal("same-frame usage crossed response identities or mutated source events", projected)
	}
}

func TestEnvelopeStreamIdentityUsageAndCancellation(t *testing.T) {
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "envelope-test", Rules: []ConversionRule{{ID: "envelope", Order: 1, Enabled: true, Phase: ConversionEvent, Action: "anthropic_usage_envelope"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cancel := range []bool{false, true} {
		state := NewConversionEventState(c, ConversionContext{Model: "chosen-model"})
		started, err := state.Push(Event{SchemaVersion: 1, Type: ResponseStarted})
		if err != nil || len(started) != 1 {
			t.Fatal(err, started)
		}
		id := started[0].Response.ID
		if id.IsZero() || started[0].Response.Model != StringValue("chosen-model") {
			t.Fatal(started)
		}
		late := &Response{ID: StringValue("late-provider-id"), Model: StringValue("late-model")}
		events, err := state.Push(Event{SchemaVersion: 1, Type: UsageUpdated, Response: late, Usage: &Usage{Input: &Counter{Count: 3, Origin: ObservedCount}}})
		if err != nil || events[0].ResponseID != id || events[0].Response.ID != id || late.ID != StringValue("late-provider-id") {
			t.Fatal(err, events)
		}
		if events, err = state.Push(Event{SchemaVersion: 1, Type: ResponseFinished}); err != nil || len(events) != 0 {
			t.Fatal(err, events)
		}
		for range 2 {
			_, err = state.Push(Event{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{Output: &Counter{Count: 5, Origin: ObservedCount}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if cancel {
			_, err = state.Push(Event{SchemaVersion: 1, Type: OperationCancelled})
			if err != nil {
				t.Fatal(err)
			}
		}
		tail, err := state.Drain()
		if err != nil {
			t.Fatal(err)
		}
		if cancel {
			if len(tail) != 0 {
				t.Fatal("cancel emitted successful terminal")
			}
		} else if len(tail) != 1 || tail[0].ResponseID != id || tail[0].Response.Usage.Input.Count != 3 || tail[0].Response.Usage.Output.Count != 5 {
			t.Fatal("late usage or identity lost, or snapshots summed", tail)
		}
	}
}
