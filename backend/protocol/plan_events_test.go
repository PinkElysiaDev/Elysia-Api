package protocol

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPlannedEventDecodingMatchesJSONBoundary(t *testing.T) {
	definition := textVerificationDefinition(t, "planned-events")
	definition.Mapping.Native.Preserve = false
	for _, direction := range []Direction{DecodeEvent, DecodeClientEvent} {
		definition.Mapping.Directions[direction] = ProtocolDirectionMapping{Effects: generationFixtureEffects(direction), Document: testDocument(direction), IsEventBatch: true, Program: readExpression("input", "")}
		compiled := compileTestDefinition(t, definition)
		if !compiled.mappings[direction].hasCoreOutputPlan {
			t.Fatal("event output plan was not selected", direction)
		}
		for _, raw := range []string{
			`{"schemaVersion":2,"type":"item.delta","itemId":"a","delta":"hello <world>"}`,
			`[{"schemaVersion":2,"type":"response.started","responseId":"r"},{"schemaVersion":2,"type":"usage.updated","usage":{"input":{"count":0,"origin":"observed"}}}]`,
			`[]`, `null`, `[{"schemaVersion":2,"type":"native","unknown":true}]`, `[{"index":"bad"}]`,
		} {
			input := Value{raw: raw}
			for _, cancelled := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				if cancelled {
					cancel()
				}
				want, wantErr := decodeEventsThroughJSON(ctx, compiled, direction, input)
				got, gotErr := compiled.decodeEventValues(ctx, direction, input, EvaluationContext{})
				cancel()
				if !reflect.DeepEqual(want, got) || fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) {
					t.Fatalf("%s changed events or contract error: got %#v / %v, want %#v / %v", direction, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

// This is the original boundary, retained only as a behavior oracle.
func decodeEventsThroughJSON(ctx context.Context, compiled *Compiled, direction Direction, input Value) ([]Event, error) {
	output, err := compiled.executeMapping(ctx, direction, input, EvaluationContext{})
	if err != nil {
		return nil, err
	}
	items, err := eventValues(output)
	if err != nil {
		return nil, err
	}
	events := make([]Event, len(items))
	for index, item := range items {
		if err := decodeContract(item.Bytes(), &events[index]); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func eventValues(value Value) ([]Value, error) {
	if value.IsObject() {
		return []Value{value}, nil
	}
	return readArray(value)
}

func TestPlannedEventArrayDoesNotRetainLargeSibling(t *testing.T) {
	definition := textVerificationDefinition(t, "event-ownership")
	definition.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeEvent), Document: testDocument(DecodeEvent), IsEventBatch: true, Program: readExpression("input", "")}
	compiled := compileTestDefinition(t, definition)
	// The retained event is large enough to be shared when measured alone, but
	// must not retain the much larger frame that also contained its sibling.
	input := Value{raw: `[{"schemaVersion":2,"type":"item.delta","itemId":"a","delta":"` + strings.Repeat("x", 90<<10) + `"},{"schemaVersion":2,"type":"item.delta","itemId":"b","delta":"` + strings.Repeat("y", 2<<20) + `"}]`}
	events, err := compiled.decodeEventValues(t.Context(), DecodeEvent, input, EvaluationContext{})
	if err != nil || len(events) != 2 {
		t.Fatal("event batch failed", err)
	}
	if sharesValueStorage(input, events[0].Delta) {
		t.Fatal("small event retained its large sibling frame")
	}
}

func TestPlannedEventResourcesOwnStorage(t *testing.T) {
	definition := textVerificationDefinition(t, "event-resource-ownership")
	definition.Mapping.Native.Preserve = false
	definition.Mapping.Capabilities[DocumentsCapability] = true
	definition.Mapping.Capabilities[RealtimeMediaCapability] = true
	definition.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeEvent), Document: testDocument(DecodeEvent), IsEventBatch: true, Program: readExpression("input", "")}
	compiled := compileTestDefinition(t, definition)
	options := EvaluationContext{State: NewEvaluationState(), Scope: Scope{Provider: "provider", Account: "account"}}
	resourceID := `"` + strings.Repeat("r", 256<<10) + `"`
	// Each ID exceeds the view-sharing threshold. Retaining either identifier
	// must not retain the other event payloads in this compound frame.
	input := Value{raw: `[{"schemaVersion":2,"type":"item.started","itemId":"resource","item":{"kind":"text","payload":"text","resources":[{"kind":"file","id":` + resourceID + `}]}},{"schemaVersion":2,"type":"media.received","media":{"type":"audio","format":"pcm","reference":{"kind":"file","id":` + resourceID + `}}},{"schemaVersion":2,"type":"item.delta","itemId":"sibling","delta":"` + strings.Repeat("x", 256<<10) + `"}]`}
	borrowed, err := compiled.decodeEventValues(t.Context(), DecodeEvent, input, options)
	if err != nil || len(borrowed) != 3 {
		t.Fatal("event fixture failed", err)
	}
	if !sharesValueStorage(input, borrowed[0].Item.Resources[0].ID) || !sharesValueStorage(input, borrowed[1].Media.Reference.ID) {
		t.Fatal("fixture did not borrow large resource identifiers")
	}
	events, err := compiled.DecodeEvents(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	target := compiled.target(DecodeEvent, options)
	target.Direction = EncodeEvent
	collector, err := NewResponseCollector(target, compiled.limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(events[0]); err != nil {
		t.Fatal(err)
	}
	for name, identifier := range map[string]Value{
		"collected resource": collector.response.Content[0].Resources[0].ID,
		"media reference":    events[1].Media.Reference.ID,
	} {
		if identifier.raw != resourceID || sharesValueStorage(input, identifier) {
			t.Errorf("%s must own independent, identical bytes", name)
		}
	}
}
