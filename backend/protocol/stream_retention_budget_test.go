package protocol

import (
	"context"
	"errors"
	"testing"
)

func combinedRetentionPackage(t *testing.T) *Compiled {
	t.Helper()
	document := eventProtocolPackage(t)
	limits := DefaultLimits()
	limits.StateItems = 3
	document.Mapping.Limits = &limits
	mapping := document.Mapping.Directions[DecodeEvent]
	mapping.Indexes = map[string]ProtocolStateIndex{"records": {KeyMode: "string", Policy: "last", Schema: ValueSchema{Type: StringType}}}
	record, identity, text, length := readExpression("input", "/record"), readExpression("input", "/identity"), readExpression("input", "/text"), readExpression("input", "/length")
	key := expressionPointer(readExpression("input", "/item_id"))
	mapping.Program.Fields["delta"] = Expression{Op: "if", When: &Expression{Op: "exists", Source: &text}, Then: &Expression{Op: "track_text", Name: "body", Source: key, Body: &text, Policy: "delta"}, Otherwise: expressionPointer(literalExpression(""))}
	steps := []Expression{
		{Op: "if", When: &Expression{Op: "exists", Source: &record}, Then: &Expression{Op: "remember", Name: "records", Source: expressionPointer(readExpression("input", "/record_key")), Body: &record}},
		{Op: "if", When: &Expression{Op: "exists", Source: &identity}, Then: &Expression{Op: "resolve_item", Source: &identity}},
		{Op: "if", When: &Expression{Op: "exists", Source: &length}, Then: &Expression{Op: "track_position", Name: "body", Source: key, Body: &length}},
	}
	for _, step := range steps {
		body := mapping.Program
		mapping.Program = Expression{Op: "let", Name: "observed", Source: expressionPointer(step), Body: &body}
	}
	document.Mapping.Directions[DecodeEvent] = mapping
	return compilePackageForTest(t, document)
}

func TestStreamRetentionCombinesIndexesIdentitiesTrackersAndRanges(t *testing.T) {
	compiled := combinedRetentionPackage(t)
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"record_key": "a", "record": "stored", "identity": map[string]any{"itemId": "base"}, "text": "text"}); err != nil {
		t.Fatal(err)
	}
	if options.State.packageStateItems != 3 {
		t.Fatal("combined categories were not charged", options.State.packageStateItems)
	}
	// Multiple aliases and tracker channels for the same canonical identity
	// consume bytes, but do not mint another dynamic item.
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"identity": map[string]any{"itemId": "base", "callId": "call", "index": 0}, "item_id": "item:base", "text": "tracked"}); err != nil || options.State.packageStateItems != 3 {
		t.Fatal("aliases were charged as distinct identities", err, options.State.packageStateItems)
	}
	key := moduleStateKey{compiled: compiled, direction: DecodeEvent}
	before, bytes := options.State.packageStates[key], options.State.packageStateBytes
	for _, fields := range []map[string]any{
		{"record_key": "b", "record": "new"},
		{"identity": map[string]any{"itemId": "new"}},
		{"item_id": "new", "text": "new"},
		{"length": 1},
	} {
		_, err := decodePrimitiveText(t, compiled, options, fields)
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 4 {
			t.Fatal("separate category escaped the aggregate budget", fields, err)
		}
		if options.State.packageStates[key] != before || options.State.packageStateItems != 3 || options.State.packageStateBytes != bytes {
			t.Fatal("failed event committed partial accounting")
		}
	}
	other := compilePackageForTest(t, semanticRangeDocument(t))
	other.limits.StateItems = 3
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("body")}
	var failure *ResourceLimitError
	if err := other.ValidateEventReferences(t.Context(), event, options); !errors.As(err, &failure) || failure.Metric != "state_items" {
		t.Fatal("another mapping obtained an independent range budget", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.DecodeEvents(ctx, primitiveFrame(t, map[string]any{"text": "canceled"}), options); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if options.State.packageStateItems != 0 || options.State.packageStateBytes != 0 {
		t.Fatal("cancel did not release combined charges")
	}
}

func TestWireRetentionCombinesEntitiesAndDeliveryRanges(t *testing.T) {
	for _, addEntity := range []bool{false, true} {
		compiled := compilePackageForTest(t, deliveryRangeEventPackage(t))
		compiled.limits.StateItems = 2
		validation := startDeliveryRangeStream(t, compiled)
		if err := validation.Consume(t.Context(), packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`)); err != nil {
			t.Fatal(err)
		}
		if err := validation.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
		text := deliveryObservation(t, 2, `[{"key":"t","text":"body","mode":"append","path":"/body"}]`, "")
		if err := validation.Consume(t.Context(), text); err != nil {
			t.Fatal("combined state within budget was refused", err)
		}
		if err := validation.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
		frame := deliveryObservation(t, 3, `[{"key":"other","text":"body","mode":"append","path":"/body"}]`, "")
		if addEntity {
			frame = packageStreamFrame(ItemStarted, 3, `,"item_id":"other"`)
		}
		var failure *ResourceLimitError
		if err := validation.Consume(t.Context(), frame); !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 3 {
			t.Fatal("wire holder granted each category a separate budget", err)
		}
		if !validation.deliveryAborted || validation.CommitDelivery() == nil {
			t.Fatal("failed aggregate check remained deliverable")
		}
		if validation.deliveryRanges.items != 0 || validation.packageStream.entityCount != 0 || len(validation.packageStream.entityOrder) != 0 {
			t.Fatal("aborted delivery retained entities or intervals")
		}
	}
}

func TestWireIdentityAliasesShareEntityBudget(t *testing.T) {
	spec := sparseIdentityStream(t)
	spec.Events["heartbeat"] = ProtocolStreamEventSpec{{Action: "control", FrameClass: "control", AllowBeforeStart: true}}
	key := &Expression{Op: "cast", Source: expressionPointer(readExpression("input", "/index")), To: StringType}
	spec.Identities = []ProtocolStreamIdentitySpec{
		{Path: "/wire_id", Type: StringType, Key: key, KeyPath: "/index", Namespace: "item", Unique: true},
		{Path: "/wire_name", Type: StringType, Key: key, KeyPath: "/index", Namespace: "item"},
	}
	state := newSparseIdentityValidation(t, spec)
	state.limits.StateItems = 2
	for _, raw := range []string{
		`{"type":"heartbeat","index":0,"wire_id":"first","wire_name":"alias"}`,
		`{"type":"message_start","message":{"id":"r"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`{"type":"heartbeat","index":1,"wire_id":"second","wire_name":"alias"}`,
	} {
		if err := state.consume(t.Context(), parseTestValue(t, raw)); err != nil {
			t.Fatal("aliases exhausted entity capacity", raw, err)
		}
	}
	if state.identityItems != 0 || state.entityCount != 2 {
		t.Fatal("namespaced aliases duplicated entity charges")
	}
	var failure *ResourceLimitError
	if err := state.consume(t.Context(), parseTestValue(t, `{"type":"heartbeat","index":2,"wire_id":"third"}`)); !errors.As(err, &failure) || failure.Observed != 3 {
		t.Fatal("unassociated identity bypassed the combined budget", err)
	}
	for _, change := range []func(*ProtocolStreamIdentitySpec){
		func(identity *ProtocolStreamIdentitySpec) { identity.Namespace = "missing" },
		func(identity *ProtocolStreamIdentitySpec) { identity.Key = nil },
	} {
		copy := *spec
		copy.Identities = append([]ProtocolStreamIdentitySpec(nil), spec.Identities...)
		change(&copy.Identities[0])
		if _, err := compilePackageStreamContract(&copy, "/stream", DefaultLimits()); err == nil {
			t.Fatal("invalid namespace alias declaration compiled")
		}
	}
}
