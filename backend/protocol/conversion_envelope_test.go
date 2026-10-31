package protocol

import (
	"strconv"
	"testing"
)

func TestDeliveryFrameUsageIsolation(t *testing.T) {
	count := func(n int64) *Usage { return &Usage{Input: &Counter{Count: n, Origin: ObservedCount}} }
	for _, fixture := range []struct{ eventVersion, responseVersion int }{
		{0, SemanticSchemaVersion},
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

func TestEnvelopeStreamIdentityUsageAndCancellation(t *testing.T) {
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "envelope-test", Rules: []ConversionRule{{ID: "envelope", Order: 1, Enabled: true, Phase: ConversionEvent, Action: "anthropic_usage_envelope"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		version int
		cancel  bool
	}{{SemanticSchemaVersion, false}, {SemanticSchemaVersion, true}, {ProtocolCoreVersion, false}, {ProtocolCoreVersion, true}} {
		state := NewConversionEventState(c, ConversionContext{Model: "chosen-model"})
		started, err := state.Push(Event{SchemaVersion: fixture.version, Type: ResponseStarted})
		if err != nil || len(started) != 1 {
			t.Fatal(err, started)
		}
		id := started[0].Response.ID
		if id.IsZero() || started[0].Response.Model != StringValue("chosen-model") || started[0].Response.SchemaVersion != fixture.version {
			t.Fatal(started)
		}
		late := &Response{ID: StringValue("late-provider-id"), Model: StringValue("late-model")}
		events, err := state.Push(Event{SchemaVersion: fixture.version, Type: UsageUpdated, Response: late, Usage: &Usage{Input: &Counter{Count: 3, Origin: ObservedCount}}})
		if err != nil || events[0].ResponseID != id || events[0].Response.ID != id || late.ID != StringValue("late-provider-id") || events[0].Response.SchemaVersion != 0 {
			t.Fatal(err, events)
		}
		if events, err = state.Push(Event{SchemaVersion: fixture.version, Type: ResponseFinished}); err != nil || len(events) != 0 {
			t.Fatal(err, events)
		}
		for range 2 {
			_, err = state.Push(Event{SchemaVersion: fixture.version, Type: UsageUpdated, Usage: &Usage{Output: &Counter{Count: 5, Origin: ObservedCount}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if fixture.cancel {
			_, err = state.Push(Event{SchemaVersion: fixture.version, Type: OperationCancelled})
			if err != nil {
				t.Fatal(err)
			}
		}
		tail, err := state.Drain()
		if err != nil {
			t.Fatal(err)
		}
		if fixture.cancel {
			if len(tail) != 0 {
				t.Fatal("cancel emitted successful terminal")
			}
		} else if len(tail) != 1 || tail[0].ResponseID != id || tail[0].Response.Usage.Input.Count != 3 || tail[0].Response.Usage.Output.Count != 5 || tail[0].Response.SchemaVersion != fixture.version {
			t.Fatal("late usage or identity lost, or snapshots summed", tail)
		}
	}
}

func TestEventProjectionResponseVersion(t *testing.T) {
	for _, action := range []string{"anthropic_usage_envelope", "response_shape", "response_envelope"} {
		rule := ConversionRule{ID: "envelope", Enabled: true, Phase: ConversionEvent, Action: action}
		if action != "anthropic_usage_envelope" {
			rule.Value = StringValue("responses")
		}
		conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "version-test", Rules: []ConversionRule{rule}})
		if err != nil {
			t.Fatal(err)
		}
		route := ConversionContext{Model: "m", Delivery: &DeliveryState{ID: StringValue("r"), Created: fixtureValue(t, 1)}}
		for _, fixture := range []struct{ eventVersion, responseVersion int }{
			{0, SemanticSchemaVersion},
			{SemanticSchemaVersion, SemanticSchemaVersion},
			{ProtocolCoreVersion, ProtocolCoreVersion},
		} {
			t.Run(action+"/"+strconv.Itoa(fixture.eventVersion), func(t *testing.T) {
				event := Event{SchemaVersion: fixture.eventVersion, Type: ResponseStarted, ResponseID: StringValue("r")}
				projected, err := conversion.Event(t.Context(), event, route, nil)
				if err != nil || projected.SchemaVersion != fixture.eventVersion || projected.Response == nil || projected.Response.SchemaVersion != fixture.responseVersion {
					t.Fatal("projection changed the event's core contract", projected, err)
				}
				if fixture.eventVersion != 0 {
					target := Target{coreVersion: fixture.eventVersion, Capabilities: CapabilitySet{UsageCapability: true}}
					if err := IssuesError(CheckEvent(projected, target, DefaultLimits())); err != nil {
						t.Fatal("synthesized response failed the existing event contract", err)
					}
				}
			})
		}
		t.Run(action+"/existing-response", func(t *testing.T) {
			event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, Response: &Response{ID: StringValue("r")}}
			projected, err := conversion.Event(t.Context(), event, route, nil)
			if err != nil || projected.Response == nil || projected.Response.SchemaVersion != 0 || event.Response.SchemaVersion != 0 {
				t.Fatal("projection corrected an existing response version", projected, err)
			}
		})
	}
}

func TestDeliveryStartKeepsEventVersion(t *testing.T) {
	rule := ConversionRule{ID: "delivery", Enabled: true, Phase: ConversionEvent, Action: "response_envelope", Value: StringValue("responses")}
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "start-version", Rules: []ConversionRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	state := NewConversionEventState(conversion, ConversionContext{Model: "m"})
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}}
	events, err := state.Push(event)
	if err != nil || len(events) != 2 || events[0].Type != ResponseStarted || events[0].SchemaVersion != ProtocolCoreVersion ||
		events[0].Response == nil || events[0].Response.SchemaVersion != ProtocolCoreVersion || events[1].SchemaVersion != ProtocolCoreVersion {
		t.Fatal("automatic response start lost the triggering event version", events, err)
	}
}
