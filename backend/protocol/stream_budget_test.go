package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestStreamBudgetRejectsMappingBeforeCommit(t *testing.T) {
	compiled := combinedRetentionPackage(t)
	state := NewEvaluationState()
	wire, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	budget := NewStreamBudget(compiled.ResourceLimits())
	if err := budget.Bind(state, wire); err != nil {
		t.Fatal(err)
	}
	defer state.Release()
	defer wire.Release()
	options := EvaluationContext{State: state}
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"record_key": "a", "record": "stored", "text": "body"}); err != nil {
		t.Fatal(err)
	}
	for _, frame := range []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`)} {
		if err := wire.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		if err := wire.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
	}
	if budget.items != 3 || state.packageStateItems != 2 || wire.packageStream.entityCount != 1 {
		t.Fatal("mapping and wire received separate capacities", budget.items)
	}
	if err := budget.Bind(state, wire); err != nil || budget.items != 3 {
		t.Fatal("rebinding charged the same holder twice", err)
	}
	key := moduleStateKey{compiled: compiled, direction: DecodeEvent}
	before, bytes := state.packageStates[key], budget.bytes
	_, err = decodePrimitiveText(t, compiled, options, map[string]any{"record_key": "b", "record": "new"})
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 4 {
		t.Fatal("aggregate item excess was accepted", err)
	}
	if state.packageStates[key] != before || state.packageStateItems != 2 || budget.items != 3 || budget.bytes != bytes {
		t.Fatal("failed mapping published state or accounting")
	}
	if err := wire.CommitDelivery(); !errors.As(err, &failure) {
		t.Fatal("failed stream acquired delivery", err)
	}
	state.Release()
	wire.Release()
	if budget.items != 0 || budget.bytes != 0 || budget.Check() == nil {
		t.Fatal("cleanup retained charges or revived the failed stream")
	}
}

func TestStreamBudgetCombinesWireIdentityAndMappingBytes(t *testing.T) {
	compiled := combinedRetentionPackage(t)
	compiled.limits.BufferBytes = 512
	state := NewEvaluationState()
	wire, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Release()
	defer wire.Release()
	budget := NewStreamBudget(compiled.ResourceLimits())
	if err := budget.Bind(state, wire); err != nil {
		t.Fatal(err)
	}
	start := fixtureValue(t, map[string]any{"kind": ResponseStarted, "response_id": strings.Repeat("r", 180), "seq": 0})
	if err := wire.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: state}
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"record_key": "a", "record": strings.Repeat("v", 200)}); err != nil {
		t.Fatal(err)
	}
	before, bytes := state.packageStates[moduleStateKey{compiled, DecodeEvent}], budget.bytes
	_, err = decodePrimitiveText(t, compiled, options, map[string]any{"record_key": "a", "record": strings.Repeat("v", 400)})
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "state_bytes" || failure.Observed <= 512 {
		t.Fatal("aggregate byte excess was accepted", err)
	}
	if state.packageStates[moduleStateKey{compiled, DecodeEvent}] != before || budget.bytes != bytes {
		t.Fatal("failed replacement changed retained state")
	}
}

func TestStreamBudgetOwnsImplicitSourceValidation(t *testing.T) {
	compiled := compilePackageForTest(t, indexedEventPackage(t))
	state := NewEvaluationState()
	budget := NewStreamBudget(compiled.ResourceLimits())
	if err := budget.Bind(state); err != nil {
		t.Fatal(err)
	}
	for _, frame := range []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`)} {
		if _, err := compiled.DecodeFrame(t.Context(), frame, EvaluationContext{State: state}); err != nil {
			t.Fatal(err)
		}
	}
	if budget.items != 2 || state.packageStateItems != 1 || len(state.sourceStreams) != 1 {
		t.Fatal("implicit source validation escaped its owner", budget.items, state.packageStateItems)
	}
	state.Release()
	if budget.items != 0 || budget.bytes != 0 {
		t.Fatal("owner release retained source validation charges")
	}
}

func TestStreamBudgetRejectsWireBeforeDelivery(t *testing.T) {
	compiled := combinedRetentionPackage(t)
	state := NewEvaluationState()
	wire, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Release()
	defer wire.Release()
	budget := NewStreamBudget(compiled.ResourceLimits())
	if err := budget.Bind(state, wire); err != nil {
		t.Fatal(err)
	}
	if _, err := decodePrimitiveText(t, compiled, EvaluationContext{State: state}, map[string]any{"record_key": "a", "record": "stored", "text": "body"}); err != nil {
		t.Fatal(err)
	}
	for _, frame := range []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ItemStarted, 1, `,"item_id":"one"`)} {
		if err := wire.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		if err := wire.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
	}
	position := wire.packageStream.delivered
	var failure *ResourceLimitError
	err = wire.Consume(t.Context(), packageStreamFrame(ItemStarted, 2, `,"item_id":"two"`))
	if !errors.As(err, &failure) || failure.Observed != 4 || wire.packageStream.delivered != position {
		t.Fatal("wire excess changed delivered position", err)
	}
	if wire.CommitDelivery() == nil || budget.items != 3 {
		t.Fatal("rejected wire committed accounting or delivery")
	}
}
