package protocol

import (
	"context"
	"errors"
	"testing"
)

func eventProtocolPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	document.Mapping.Capabilities[UsageCapability] = true
	document.Mapping.Native.Preserve = true
	stream := Value{raw: `{"discriminator":"/kind","responseId":"/response_id","itemId":"/item_id","sequence":"/seq","events":{
		"response.started":{"action":"response_start"},"item.started":{"action":"item_start"},
		"item.delta":{"action":"text_delta","text":"/text"},"item.finished":{"action":"item_finish"},
		"usage.updated":{"action":"usage","usage":{"input":"/usage/input","output":"/usage/output","total":"/usage/total"}},
		"response.finished":{"action":"response_finish"},"operation.failed":{"action":"failure","error":"/error"},"operation.cancelled":{"action":"cancel"}}}`}
	if err := stream.Decode(&document.Spec.Stream); err != nil {
		t.Fatal(err)
	}
	isType := func(kind EventType) Expression {
		return Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(kind)}}
	}
	when := func(condition, value Expression) Expression {
		return Expression{Op: "if", When: &condition, Then: &value, Otherwise: &Expression{Op: "omit"}}
	}
	usageIn, usageOut := map[string]Expression{}, map[string]Expression{}
	for _, name := range []string{"input", "output", "total"} {
		count := readExpression("input", "/usage/"+name)
		usageIn[name] = when(Expression{Op: "exists", Source: &count}, objectExpression(map[string]Expression{
			"count": count, "origin": literalExpression("observed"),
		}))
		usageOut[name] = readExpression("input", "/usage/"+name+"/count")
	}
	decode := objectExpression(map[string]Expression{
		"schemaVersion": literalExpression(ProtocolCoreVersion), "type": readExpression("input", "/kind"),
		"responseId": readExpression("input", "/response_id"), "itemId": readExpression("input", "/item_id"),
		"sequence": readExpression("input", "/seq"), "delta": readExpression("input", "/text"),
		"error": readExpression("input", "/error"),
		"item":  when(isType(ItemStarted), objectExpression(map[string]Expression{"kind": literalExpression("text")})),
		"usage": when(isType(UsageUpdated), objectExpression(usageIn)),
	})
	usage := readExpression("input", "/usage")
	encode := objectExpression(map[string]Expression{
		"kind": readExpression("input", "/type"), "response_id": readExpression("input", "/responseId"),
		"item_id": readExpression("input", "/itemId"), "seq": readExpression("input", "/sequence"),
		"text": readExpression("input", "/delta"), "error": readExpression("input", "/error"),
		"usage": when(Expression{Op: "exists", Source: &usage}, objectExpression(usageOut)),
	})
	schema := ValueSchema{Type: ObjectType, AllowUnknown: true, Required: []string{"kind", "response_id", "seq"}, Properties: map[string]ValueSchema{
		"kind": {Type: StringType}, "response_id": {Type: StringType}, "seq": {Type: IntegerType},
	}}
	for direction, program := range map[Direction]Expression{DecodeEvent: decode, EncodeEvent: encode} {
		document.Spec.WireContracts[direction] = WireContractSpec{Schema: &schema}
		document.Mapping.Directions[direction] = ProtocolDirectionMapping{Program: program, Effects: []ProtocolSemanticEffect{
			{ID: "response-id", Kind: "emit", Path: "/responseId", Target: "/response_id"},
		}}
	}
	return document
}

func TestPackageEventRuntime(t *testing.T) {
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	options := EvaluationContext{Scope: Scope{Provider: "local", Account: "synthetic"}, State: NewEvaluationState()}
	source, err := compiled.NewWireInputStreamValidation(options.Scope)
	if err != nil {
		t.Fatal(err)
	}
	final, err := compiled.NewWireStreamValidation(options.Scope)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewResponseCollector(compiled.Target(EncodeEvent, options.Scope), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	frames := []string{
		`{ "kind":"response.started", "response_id":"r", "seq":0, "unknown":{"n":9007199254740993} }`,
		`{"kind":"item.started","response_id":"r","item_id":"text","seq":1}`,
		`{"kind":"item.delta","response_id":"r","item_id":"text","seq":2,"text":"hello"}`,
		`{"kind":"usage.updated","response_id":"r","seq":3,"usage":{"input":7}}`,
		`{"kind":"item.finished","response_id":"r","item_id":"text","seq":4}`,
		`{"kind":"response.finished","response_id":"r","seq":5}`,
		`{"kind":"usage.updated","response_id":"r","seq":6,"usage":{"input":7,"output":2,"total":9}}`,
	}
	for _, raw := range frames {
		frame := Value{raw: raw}
		if err := source.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		events, err := compiled.DecodeEvents(t.Context(), frame, options)
		if err != nil || len(events) != 1 {
			t.Fatal("single event decode", events, err)
		}
		event := events[0]
		if event.SchemaVersion != ProtocolCoreVersion {
			t.Fatal("event version was replaced", event.SchemaVersion)
		}
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
		output, err := compiled.EncodeEvent(t.Context(), event, options)
		if err != nil || output != frame {
			t.Fatal("native frame bytes changed", output.JSONString(), err)
		}
		if err := final.Consume(t.Context(), output); err != nil {
			t.Fatal(err)
		}
		core, issues := compiled.Execute(t.Context(), DecodeEvent, frame, options)
		if err := IssuesError(issues); err != nil {
			t.Fatal("raw event decode", err)
		}
		if _, issues := compiled.Execute(t.Context(), EncodeEvent, core, options); len(issues) != 0 {
			t.Fatal("raw event encode", issues)
		}
	}
	if err := errors.Join(source.Finish(), final.Finish()); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil || response.SchemaVersion != ProtocolCoreVersion || response.Usage.Total.Count != 9 || len(response.Content) != 1 || response.Content[0].Payload != StringValue("hello") {
		t.Fatal("collection lost text, usage or core version", response, err)
	}
	if frames, err := compiled.FinishEvents(t.Context(), options); err != nil || len(frames) != 0 {
		t.Fatal("stateless mapping invented a terminal", frames, err)
	}
}

func TestPackageEventCoreVersion(t *testing.T) {
	for _, field := range []string{"schemaVersion", "request", "response"} {
		t.Run(field, func(t *testing.T) {
			document := eventProtocolPackage(t)
			value := literalExpression(1)
			if field != "schemaVersion" {
				value = objectExpression(map[string]Expression{"schemaVersion": value})
			}
			document.Mapping.Directions[DecodeEvent].Program.Fields[field] = value
			compiled := compilePackageForTest(t, document)
			_, err := compiled.DecodeEvents(t.Context(), Value{raw: `{"kind":"response.started","response_id":"r","seq":0}`}, EvaluationContext{})
			assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion")
		})
	}
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	target := compiled.Target(EncodeEvent, Scope{})
	delete(target.Capabilities, TextCapability)
	if !compiled.Capabilities(EncodeEvent)[TextCapability] {
		t.Fatal("public target can mutate the compiled revision")
	}
	event := Event{SchemaVersion: SemanticSchemaVersion, Type: ResponseStarted}
	_, err := compiled.EncodeEvent(t.Context(), event, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = compiled.DecodeEvents(ctx, Value{raw: `{"kind":"response.started","response_id":"r","seq":0}`}, EvaluationContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("event decoding lost cancellation", err)
	}
}
