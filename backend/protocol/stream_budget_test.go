package protocol

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestStreamBudgetCombinesCollectorsAndReplays(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	source, err := NewEventReplay(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewResponseCollector(reasoningCollectionTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	defer collector.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(source, collector, collector.replay); err != nil {
		t.Fatal(err)
	}
	start := reasoningCollectionEvent(ItemStarted, "text", "", "", &Node{Kind: TextNode})
	if _, err := source.Consume(start); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	metadata := Event{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Metadata: []ResponseMetadata{{Codec: "custom", Location: "response", Name: "a", Value: StringValue("first")}}}
	if _, _, err := collector.Consume(metadata); err != nil || budget.items != 3 {
		t.Fatal("collector counted its replay twice", budget.items, err)
	}
	metadata.Metadata[0].Name = "b"
	var failure *ResourceLimitError
	if _, delta, err := collector.Consume(metadata); !errors.As(err, &failure) || delta != "" || failure.Observed != 4 {
		t.Fatal("collector and source replay acquired separate budgets", err)
	}
	source.Release()
	collector.Release()
	if budget.items != 0 || budget.bytes != 0 || len(budget.sequences) != 0 {
		t.Fatal("released owners retained accounting or sequence history")
	}
}

func TestStreamBudgetHistoryYieldsToOtherOwners(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	left, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Release()
	defer right.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(left, right); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 6; sequence++ {
		event := Event{SchemaVersion: 1, Type: MetadataUpdated, Sequence: coreInteger(sequence)}
		for _, replay := range []*EventReplay{left, right} {
			if _, err := replay.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if left.sequence.count+right.sequence.count-2 > limits.StateItems {
		t.Fatal("history lanes retained independent capacities")
	}
	start := messageEvent(ItemStarted, "one", "", &Node{Kind: TextNode})
	for _, replay := range []*EventReplay{left, right} {
		if _, err := replay.Consume(start); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := left.Consume(messageEvent(ItemStarted, "two", "", &Node{Kind: TextNode})); err != nil {
		t.Fatal(err)
	}
	if left.sequence.count != 1 || right.sequence.count != 1 || budget.items != 3 {
		t.Fatal("optional history did not yield to mandatory state")
	}
	for _, replay := range []*EventReplay{left, right} {
		latest := Event{SchemaVersion: 1, Type: MetadataUpdated, Sequence: coreInteger(6)}
		if accepted, err := replay.Consume(latest); err != nil || accepted {
			t.Fatal("history eviction lost the latest replay receipt", accepted, err)
		}
	}
}

func TestStreamBudgetHistorySharesBytes(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 256
	replay, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(replay); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 20; sequence++ {
		if _, err := replay.Consume(Event{SchemaVersion: 1, Type: MetadataUpdated, Sequence: coreInteger(sequence)}); err != nil {
			t.Fatal(err)
		}
	}
	if replay.sequence.count*retainedSequenceBytes > limits.BufferBytes || replay.sequence.count != 3 {
		t.Fatal("history payload escaped the retained byte budget", replay.sequence.count)
	}
	if _, err := replay.Consume(Event{SchemaVersion: 1, Type: MetadataUpdated, Sequence: coreInteger(17)}); err == nil {
		t.Fatal("expired sequence was accepted")
	}
}

func TestStreamBudgetIncludesPendingCarrierRendering(t *testing.T) {
	compiled := compilePackageForTest(t, continuationTestPackage(t))
	source := Identity{DefinitionID: "source", Family: "source", WireVersion: "1"}
	scope := Scope{Provider: "fixture", Model: "m"}
	node := Node{Kind: TextNode, Payload: StringValue("hi")}
	codec, err := NewContinuationCodec([]byte("aggregate-carrier-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := codec.SealState(ContinuationRecord{Version: 1, ID: "one", Protocol: source, Scope: scope, Digest: ContinuationNodeDigest(node), Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	view := fixtureValue(t, []map[string]string{{"id": "one", "token": sealed.Token()}})
	limits := DefaultLimits()
	limits.BufferBytes = sealed.bytes + view.Len()
	state := NewEvaluationState()
	binding, err := compiled.BindContinuation(EncodeResponse, source, scope, true)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Release()
	defer binding.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(state, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := decodePrimitiveText(t, combinedRetentionPackage(t), EvaluationContext{State: state}, map[string]any{"record_key": "a", "record": "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := binding.Append(sealed); err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{Scope: scope}).WithContinuation(binding)
	_, err = compiled.EncodeContinuationResponse(t.Context(), fixtureValue(t, map[string]string{"reply": "hi"}), options)
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "state_bytes" {
		t.Fatal("rendering used capacity already held by another owner", err)
	}
	if binding.isRendering || binding.tokenView.bytes != 0 || binding.committed != 0 || binding.CommitDelivery() == nil {
		t.Fatal("failed carrier reservation published a delivery batch")
	}
	state.Release()
	binding.Release()
	if budget.items != 0 || budget.bytes != 0 {
		t.Fatal("released carrier retained charges")
	}
}

func TestStreamBudgetIncludesWaitingConversionEvents(t *testing.T) {
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "shared-wait", Rules: []ConversionRule{{ID: "wait", Enabled: true, Phase: ConversionEvent, Action: "buffer_node", Match: ConversionMatch{NodeKind: ToolCallNode}}}})
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.StateItems = 3
	replay, err := NewEventReplay(replayTarget(), limits)
	if err != nil {
		t.Fatal(err)
	}
	waiting := NewConversionEventState(conversion, ConversionContext{})
	defer replay.Release()
	defer waiting.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(replay, waiting); err != nil {
		t.Fatal(err)
	}
	start := toolStart()
	if _, err := replay.Consume(start); err != nil {
		t.Fatal(err)
	}
	delta := Event{SchemaVersion: 1, Type: ItemDelta, ItemID: start.ItemID, Delta: StringValue(`{"n":`)}
	for _, event := range []Event{start, delta} {
		if output, err := waiting.PushContext(t.Context(), event); err != nil || len(output) != 0 {
			t.Fatal("waiting input did not fit the shared capacity", output, err)
		}
	}
	var failure *ResourceLimitError
	if output, err := waiting.PushContext(t.Context(), delta); !errors.As(err, &failure) || failure.Observed != 4 || len(output) != 0 {
		t.Fatal("waiting events escaped shared item accounting", output, err)
	}
	if output, err := waiting.Drain(); err == nil || len(output) != 0 {
		t.Fatal("failed waiting input was published", output, err)
	}
	replay.Release()
	waiting.Release()
	if budget.items != 0 || budget.bytes != 0 || len(budget.sequences) != 0 {
		t.Fatal("waiting state survived release")
	}
}

func TestStreamBudgetIncludesDeferredTerminal(t *testing.T) {
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "shared-tail", Rules: []ConversionRule{{ID: "envelope", Enabled: true, Phase: ConversionEvent, Action: "anthropic_usage_envelope"}}})
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.BufferBytes = 1024
	state := NewEvaluationState()
	waiting := NewConversionEventState(conversion, ConversionContext{Model: "fixture"})
	defer state.Release()
	defer waiting.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(state, waiting); err != nil {
		t.Fatal(err)
	}
	if _, err := decodePrimitiveText(t, combinedRetentionPackage(t), EvaluationContext{State: state}, map[string]any{"record_key": "a", "record": strings.Repeat("v", 400)}); err != nil {
		t.Fatal(err)
	}
	if _, err := waiting.PushContext(t.Context(), Event{SchemaVersion: 1, Type: ResponseStarted, ResponseID: StringValue("r")}); err != nil {
		t.Fatal(err)
	}
	terminal := Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("t", 650))}}}}
	var failure *ResourceLimitError
	if output, err := waiting.PushContext(t.Context(), terminal); !errors.As(err, &failure) || failure.Metric != "state_bytes" || len(output) != 0 {
		t.Fatal("deferred terminal obtained an independent buffer", output, err)
	}
	if output, err := waiting.Drain(); err == nil || len(output) != 0 {
		t.Fatal("oversized terminal drained successfully", output, err)
	}
}
