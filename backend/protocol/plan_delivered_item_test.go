package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestDeliveredTextResumesOnlyCommittedState(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Spec.Documents["client-event"].Stream.Namespaces["item"] = ProtocolStreamNamespaceSpec{KeyType: IntegerType}
	mapping := document.Mapping.Directions[EncodeEvent]
	mapping.Program.Fields["item_id"] = literalExpression(7)
	mapping.Program.Fields["text"] = Expression{Op: "track_text", Name: "body", Source: expressionPointer(literalExpression("target")), Body: expressionPointer(readExpression("input", "/delta")), Policy: "snapshot"}
	mapping.Program.Fields["observed"] = Expression{Op: "read_delivered_item", Namespace: "item", Source: expressionPointer(literalExpression("7"))}
	mapping.Program.Fields["maximum"] = readExpression("runtime", "/delivered/maxItemIndex")
	mapping.Program = Expression{Op: "let", Name: "resumed", Source: &Expression{Op: "resume_text", Namespace: "item", Name: "body", Source: expressionPointer(literalExpression("target")), Body: expressionPointer(literalExpression("7"))}, Body: expressionPointer(mapping.Program)}
	mapping.Observe = &Expression{Op: "omit"}
	document.Mapping.Directions[EncodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	if compiled.mappings[EncodeEvent].outputPlan.hasOutputStreamRead {
		t.Fatal("item continuation forced native frames through rendering")
	}
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ItemStarted, 1, `,"item_id":7`)} {
		if err := validation.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		if err := validation.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
	}
	if err := validation.Consume(t.Context(), packageStreamFrame(ItemDelta, 2, `,"item_id":7,"text":"prefix"`)); err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{State: NewEvaluationState()}).WithOutputStream(validation)
	defer options.State.Release()
	event := Event{SchemaVersion: compiled.CoreVersion(), Type: ItemDelta, ResponseID: StringValue("r"), ItemID: StringValue("target"), Sequence: coreInteger(3), Delta: StringValue("prefixsuffix")}
	if _, err := compiled.EncodeEvent(t.Context(), event, options); err == nil || !strings.Contains(err.Error(), "completely delivered") {
		t.Fatal("unsent text was admitted as a delivered prefix", err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	nativeEvent := event
	nativeEvent.Native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: DecodeEvent}, Value: packageStreamFrame(ItemDelta, 3, `,"item_id":7,"text":"original"`)}
	nativeOptions := (EvaluationContext{State: NewEvaluationState()}).WithOutputStream(validation)
	_, err = compiled.EncodeEvent(t.Context(), nativeEvent, nativeOptions)
	nativeOptions.State.Release()
	var nativeFailure *ConversionError
	if !errors.As(err, &nativeFailure) || len(nativeFailure.Issues) != 1 || nativeFailure.Issues[0].Code != UnsupportedNative || !strings.Contains(nativeFailure.Issues[0].Reason, "automatic native reconciliation") {
		t.Fatal("native reconciliation bypassed a delivered-item dependency", err)
	}
	rewritten := event
	rewritten.Delta = StringValue("changed")
	if _, err := compiled.EncodeEvent(t.Context(), rewritten, options); err == nil {
		t.Fatal("resumption accepted a rewritten prefix")
	}
	wire, err := compiled.EncodeEvent(t.Context(), event, options)
	if err != nil {
		t.Fatal("failed resumption changed the next transaction", err)
	}
	fields, _ := wire.ReadObject()
	if !fields["text"].IsString("suffix") || fields["maximum"].raw != "7" || !equalValues(fields["observed"], Value{raw: `{"entity":"text","payload":"text","isFinished":false,"index":7}`}) {
		t.Fatal("resumption lost delivered item facts or repeated the prefix", wire)
	}
	item := validation.packageStream.entities[validation.packageStream.contract.namespaceSlots["item"]]["7"]
	if item.text.Length() != len("prefix") {
		t.Fatal("the resumed tracker shared mutable digest state with validation")
	}
	if suffix, err := item.text.snapshotSuffix("prefix"); err != nil || suffix != "" {
		t.Fatal("the resumed tracker changed the validator's digest", suffix, err)
	}
	for _, scope := range []Scope{{}, {Account: "other"}} {
		unbound := EvaluationContext{Scope: scope, State: NewEvaluationState()}
		if scope.Account != "" {
			unbound = unbound.WithOutputStream(validation)
		}
		if _, err := compiled.EncodeEvent(t.Context(), event, unbound); err == nil {
			t.Fatal("resumption read an unbound or differently scoped stream")
		}
		unbound.State.Release()
	}
	if err := validation.Consume(t.Context(), packageStreamFrame(ItemStarted, 4, `,"item_id":7`)); err == nil {
		t.Fatal("invalid duplicate start fixture succeeded")
	}
	if _, err := bindEvaluationRuntime(compiled, EncodeEvent, options).deliveredItems(); err == nil {
		t.Fatal("failed validation exposed mutable item state")
	}
	validation, err = compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), parseTestValue(t, `{}`)); err == nil {
		t.Fatal("invalid wire schema fixture succeeded")
	}
	options = options.WithOutputStream(validation)
	if _, err := bindEvaluationRuntime(compiled, EncodeEvent, options).deliveredItems(); err == nil {
		t.Fatal("schema rejection was missed before the lifecycle observer")
	}
}
