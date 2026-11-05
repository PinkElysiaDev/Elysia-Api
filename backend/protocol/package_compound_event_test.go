package protocol

import (
	"slices"
	"testing"
)

func compoundEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	stream := document.Spec.Documents["provider-event"].Stream
	for _, tag := range []EventType{ResponseStarted, ItemStarted, ItemDelta, ItemFinished, UsageUpdated, ResponseFinished} {
		stream.Events["combined"] = append(stream.Events["combined"], stream.Events[string(tag)]...)
	}

	mapping := document.Mapping.Directions[DecodeEvent]
	single := mapping.Program
	mapping.Program = Expression{Op: "array"}
	for _, kind := range []EventType{ResponseStarted, ItemStarted, ItemDelta, ItemFinished, UsageUpdated, ResponseFinished} {
		fields := map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "type": literalExpression(kind), "responseId": readExpression("input", "/response_id")}
		if kind == ItemStarted || kind == ItemDelta || kind == ItemFinished {
			fields["itemId"] = readExpression("input", "/item_id")
		}
		switch kind {
		case ItemStarted:
			fields["item"] = objectExpression(map[string]Expression{"kind": literalExpression(TextNode)})
		case ItemDelta:
			fields["delta"] = readExpression("input", "/text")
		case UsageUpdated:
			fields["usage"] = *single.Fields["usage"].Then
		}
		mapping.Program.Items = append(mapping.Program.Items, objectExpression(fields))
	}
	mapping.IsEventBatch = true
	mapping.Effects = []ProtocolSemanticEffect{{ID: "batch-response", Kind: "emit", Path: "/0/responseId", Target: "/response_id"}}
	document.Mapping.Directions[DecodeEvent] = mapping
	return document
}

func TestPackageCompoundFramePreservesOneNativeCarrier(t *testing.T) {
	document := compoundEventPackage(t)
	compiled := compilePackageForTest(t, document)
	input := parseTestValue(t, `{ "kind":"combined","response_id":"r","item_id":"t","seq":0,"text":"hello","usage":{"input":2,"output":1,"total":3},"unknown":9007199254740993 }`)
	source, _ := compiled.NewWireInputStreamValidation(Scope{})
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	frame, err := source.DecodeFrame(t.Context(), input, options)
	if err != nil || len(frame.Events) != 6 {
		t.Fatal("compound frame lost actions", frame, err)
	}
	collector, _ := NewResponseCollector(compiled.Target(EncodeEvent, Scope{}), DefaultLimits())
	for _, event := range frame.Events {
		if event.Native != nil {
			t.Fatal("native frame was duplicated on semantic members")
		}
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	response, err := collector.Finish()
	if err != nil || response.Usage.Total.Count != 3 || !response.Content[0].Payload.IsString("hello") {
		t.Fatal(response, err)
	}
	encoded, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{})
	if err != nil || len(encoded) != 1 || encoded[0] != input {
		t.Fatal("compound carrier was changed or emitted repeatedly", encoded, err)
	}
	final, _ := compiled.NewWireStreamValidation(Scope{})
	if err := final.Consume(t.Context(), encoded[0]); err != nil {
		t.Fatal(err)
	}
	if err := final.CommitDelivery(); err != nil || final.packageStream.delivered.sequence != 0 || !final.packageStream.delivered.isTerminal {
		t.Fatal("frame actions advanced the wire sequence more than once", err)
	}
	if err := source.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := final.Finish(); err != nil {
		t.Fatal(err)
	}

	targetDocument := eventProtocolPackage(t)
	targetDocument.Spec.ID = "expanded-output"
	targetDocument.Spec.Family = "expanded-output"
	for _, name := range []string{"provider-event", "client-event"} {
		wire := targetDocument.Spec.Documents[name]
		wire.Stream.Sequence = ""
		wire.Schema.Required = slices.DeleteFunc(wire.Schema.Required, func(name string) bool { return name == "seq" })
	}
	target := compilePackageForTest(t, targetDocument)
	expanded, err := target.EncodeFrame(t.Context(), frame, EvaluationContext{})
	if err != nil || len(expanded) != len(frame.Events) {
		t.Fatal("declared cross-wire expansion failed", expanded, err)
	}
	validation, _ := target.NewWireStreamValidation(Scope{})
	for _, wire := range expanded {
		if err := validation.Consume(t.Context(), wire); err != nil {
			t.Fatal(err)
		}
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	frame.Events[2].Delta = StringValue("changed")
	if _, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{}); err == nil {
		t.Fatal("changed compound semantics bypassed native ownership")
	}
}

func TestPackageCompoundFrameRejectsHiddenFailure(t *testing.T) {
	input := parseTestValue(t, `{"kind":"combined","response_id":"r","item_id":"t","seq":0,"text":"hello","usage":{"input":2,"output":1,"total":3}}`)
	document := compoundEventPackage(t)
	document.Mapping.Directions[DecodeEvent].Program.Items[1].Fields["schemaVersion"] = literalExpression(1)
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	_, err := compiled.DecodeFrame(t.Context(), input, options)
	assertPackageRuntimeIssue(t, err, InvalidInput, "/1/schemaVersion")
	document = compoundEventPackage(t)
	actions := document.Spec.Documents["provider-event"].Stream.Events["combined"]
	actions[1], actions[2] = actions[2], actions[1]
	compiled = compilePackageForTest(t, document)
	validation, _ := compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), input); err == nil {
		t.Fatal("semantic mapping could conceal the wire's orphan delta")
	}
}
