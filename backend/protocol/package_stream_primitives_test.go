package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func streamPrimitivePackage(t *testing.T, expression Expression) *Compiled {
	t.Helper()
	document := eventProtocolPackage(t)
	mapping := document.Mapping.Directions[DecodeEvent]
	mapping.Program.Fields["delta"] = expression
	document.Mapping.Directions[DecodeEvent] = mapping
	return compilePackageForTest(t, document)
}

func primitiveFrame(t *testing.T, fields map[string]any) Value {
	t.Helper()
	frame := map[string]any{"kind": ItemDelta, "response_id": "r", "item_id": "i", "seq": 0}
	for key, value := range fields {
		frame[key] = value
	}
	return fixtureValue(t, frame)
}

func decodePrimitiveText(t *testing.T, compiled *Compiled, options EvaluationContext, fields map[string]any) (string, error) {
	t.Helper()
	events, err := compiled.DecodeEvents(t.Context(), primitiveFrame(t, fields), options)
	if err != nil {
		return "", err
	}
	if len(events) != 1 {
		t.Fatal("primitive produced extra events", events)
	}
	return readString(events[0].Delta)
}

func TestPackageTextTrackerCommitsOnlySuccessfulEvents(t *testing.T) {
	delta := Expression{Op: "track_text", Name: "body", Source: expressionPointer(readExpression("input", "/item_id")), Body: expressionPointer(readExpression("input", "/text")), Policy: "delta"}
	snapshot := delta
	snapshot.Policy = "snapshot"
	compiled := streamPrimitivePackage(t, Expression{Op: "if", When: expressionPointer(readExpression("input", "/snapshot")), Then: &snapshot, Otherwise: &delta})
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, fixture := range []struct {
		text, want          string
		snapshot, isInvalid bool
	}{
		{"hello", "hello", false, false},
		{"discarded", "", false, true},
		{"hello!", "!", true, false},
		{"hello!", "", true, false},
	} {
		fields := map[string]any{"text": fixture.text, "snapshot": fixture.snapshot}
		if fixture.isInvalid {
			fields["kind"] = "not-an-event"
		}
		text, err := decodePrimitiveText(t, compiled, options, fields)
		if fixture.isInvalid {
			if err == nil {
				t.Fatal("invalid core event committed its text")
			}
		} else if err != nil || text != fixture.want {
			t.Fatal(text, err, fixture)
		}
	}
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"text": "changed", "snapshot": true}); err == nil {
		t.Fatal("rewritten text prefix accepted")
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}].primitives
	if retained.texts[streamTrackKey{channel: "body", item: "i"}].Length() != len("hello!") {
		t.Fatal("failed events advanced the digest")
	}
}

func TestPackageToolTrackerWithheldArgumentsAndRollback(t *testing.T) {
	compiled := streamPrimitivePackage(t, Expression{Op: "track_tool", Name: "arguments", Source: expressionPointer(readExpression("input", "/item_id")), Body: expressionPointer(readExpression("input", "/update"))})
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, fixture := range []struct {
		update, want string
		isInvalid    bool
	}{
		{`{"kind":"json","text":"{\"n\":","snapshot":false,"done":false,"emit":false}`, "", false},
		{`{"callId":"late-call","text":"1}","snapshot":false,"done":false,"emit":true}`, `{"n":1}`, false},
		{`{"text":"{ \"n\" : 1 }","snapshot":true,"done":false,"emit":true}`, "", false},
		{`{"snapshot":false,"done":true,"emit":true}`, "", false},
		{`{"text":"{\"n\":1}","snapshot":true,"done":true,"emit":true}`, "", false},
		{`{"text":"more","snapshot":false,"done":false,"emit":true}`, "", true},
	} {
		text, err := decodePrimitiveText(t, compiled, options, map[string]any{"update": json.RawMessage(fixture.update)})
		if fixture.isInvalid {
			if err == nil {
				t.Fatal("completed tool accepted new content")
			}
		} else if err != nil || text != fixture.want {
			t.Fatal(text, err, fixture)
		}
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}].primitives
	tool := retained.tools[streamTrackKey{channel: "arguments", item: "i"}]
	if retained.toolBuffered != 0 || tool.input.Len() != 0 || !tool.isDone {
		t.Fatal("completed tool retained argument bytes")
	}
	for _, fixture := range []struct {
		update, want string
		isInvalid    bool
	}{
		{`{"kind":"json","text":"{\"n\":","snapshot":false,"done":false,"emit":false}`, "", false},
		{`{"text":"}","snapshot":false,"done":true,"emit":true}`, "", true},
		{`{"text":"2}","snapshot":false,"done":true,"emit":true}`, `{"n":2}`, false},
	} {
		text, err := decodePrimitiveText(t, compiled, options, map[string]any{"item_id": "other", "update": json.RawMessage(fixture.update)})
		if fixture.isInvalid {
			if err == nil {
				t.Fatal("invalid completed JSON was accepted")
			}
		} else if err != nil || text != fixture.want {
			t.Fatal("failed completion changed the retained prefix", text, err)
		}
	}
}

func TestPackageItemIdentityAliasesAreTransactional(t *testing.T) {
	document := eventProtocolPackage(t)
	mapping := document.Mapping.Directions[DecodeEvent]
	resolved := Expression{Op: "resolve_item", Source: expressionPointer(readExpression("input", "/identity"))}
	mapping.Program.Fields["itemId"] = Expression{Op: "let", Name: "identity", Source: &resolved, Body: expressionPointer(Expression{Op: "read", From: "binding", Name: "identity", Path: "/key"})}
	document.Mapping.Directions[DecodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, fixture := range []struct {
		identity, kind, want string
		isInvalid            bool
	}{
		{`{"index":0}`, "item.delta", "index:0", false},
		{`{"index":0,"itemId":"discarded"}`, "not-an-event", "", true},
		{`{"index":1,"itemId":"discarded"}`, "item.delta", "item:discarded", false},
		{`{"index":0,"callId":"call"}`, "item.delta", "index:0", false},
		{`{"callId":"call"}`, "item.delta", "index:0", false},
		{`{"index":1,"callId":"call"}`, "item.delta", "", true},
	} {
		events, err := compiled.DecodeEvents(t.Context(), primitiveFrame(t, map[string]any{"identity": json.RawMessage(fixture.identity), "kind": fixture.kind, "text": "x"}), options)
		if fixture.isInvalid {
			if err == nil {
				t.Fatal("conflicting or invalid event was accepted")
			}
		} else if err != nil || len(events) != 1 || !events[0].ItemID.IsString(fixture.want) {
			t.Fatal("late alias changed identity", events, err, fixture.want)
		}
	}
}

func TestPackageStreamSharedRetentionBudget(t *testing.T) {
	const retainedPart = 800
	document := eventProtocolPackage(t)
	limits := DefaultLimits()
	limits.BufferBytes = 4096
	document.Mapping.Limits = &limits
	mapping := document.Mapping.Directions[DecodeEvent]
	slot := readExpression("input", "/slot")
	mapping.State = map[string]ProtocolStateSlot{"metadata": {
		Schema: ValueSchema{Type: StringType}, Initial: StringValue(strings.Repeat("m", retainedPart)),
		Update: Expression{Op: "if", When: &Expression{Op: "exists", Source: &slot}, Then: &slot, Otherwise: expressionPointer(readExpression("state", "/metadata"))},
	}}
	mapping.Indexes = map[string]ProtocolStateIndex{"records": {KeyMode: "string", Policy: "last", Schema: ValueSchema{Type: StringType}}}
	update, record, identity := readExpression("input", "/update"), readExpression("input", "/record"), readExpression("input", "/identity")
	mapping.Program.Fields["delta"] = Expression{Op: "if", When: &Expression{Op: "exists", Source: &update},
		Then: &Expression{Op: "track_tool", Name: "arguments", Source: expressionPointer(literalExpression("i")), Body: &update}, Otherwise: expressionPointer(literalExpression(""))}
	store := Expression{Op: "if", When: &Expression{Op: "exists", Source: &record}, Then: &Expression{Op: "remember", Name: "records", Source: expressionPointer(literalExpression("record")), Body: &record}}
	resolve := Expression{Op: "if", When: &Expression{Op: "exists", Source: &identity}, Then: &Expression{Op: "resolve_item", Source: &identity}}
	body := mapping.Program
	mapping.Program = Expression{Op: "let", Name: "stored", Source: &store, Body: &Expression{Op: "let", Name: "resolved", Source: &resolve, Body: &body}}
	document.Mapping.Directions[DecodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	observation := func(text string, emit, done bool) map[string]any {
		return map[string]any{"kind": "text", "callId": "call", "text": text, "snapshot": false, "done": done, "emit": emit}
	}
	for _, fixture := range []struct {
		name   string
		fields map[string]any
	}{
		{"index", map[string]any{"record": strings.Repeat("r", 2500)}},
		{"slot", map[string]any{"slot": strings.Repeat("s", 2500)}},
		{"tool", map[string]any{"update": observation(strings.Repeat("t", 1800), false, false)}},
		{"aliases", map[string]any{"identity": map[string]any{"itemId": strings.Repeat("a", 1900)}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := EvaluationContext{State: NewEvaluationState()}
			if _, err := decodePrimitiveText(t, compiled, options, map[string]any{
				"record": strings.Repeat("r", retainedPart), "identity": map[string]any{"itemId": "base"},
				"update": observation(strings.Repeat("t", retainedPart), false, false),
			}); err != nil {
				t.Fatal("combined state within the shared budget", err)
			}
			key := moduleStateKey{compiled: compiled, direction: DecodeEvent}
			before, bytes := options.State.packageStates[key], options.State.packageStateBytes
			_, err := decodePrimitiveText(t, compiled, options, fixture.fields)
			var limit *ResourceLimitError
			if !errors.As(err, &limit) || limit.Limit != limits.BufferBytes {
				t.Fatal("individually bounded payload exceeded the combined state budget", err)
			}
			if options.State.packageStates[key] != before || options.State.packageStateBytes != bytes {
				t.Fatal("failed retention committed state or changed its shared charge")
			}
			output, err := decodePrimitiveText(t, compiled, options, map[string]any{"update": observation("", true, true)})
			if err != nil || len(output) != retainedPart || options.State.packageStateBytes != bytes-retainedPart {
				t.Fatal("tool completion did not release exactly its buffered payload", err, options.State.packageStateBytes, bytes)
			}
			options.State.Release()
			if options.State.packageStateBytes != 0 || options.State.packageStateItems != 0 || len(options.State.packageStates) != 0 {
				t.Fatal("release retained a combined state charge")
			}
		})
	}
}
