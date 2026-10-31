package protocol

import (
	"strings"
	"testing"
)

func controlProtocolPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := auxiliaryProtocolPackage(t)
	document.Mapping.Capabilities[ControlStateCapability] = true
	document.Spec.Documents["provider-event"].Stream.Events["state"] = ProtocolStreamEventSpec{{Action: "observe", FrameClass: "state", ResponseIndependent: true, AllowBeforeStart: true}}
	control := objectExpression(map[string]Expression{
		"schemaVersion": literalExpression(ProtocolCoreVersion), "type": literalExpression(ControlUpdated),
		"responseId": readExpression("input", "/response_id"), "sequence": readExpression("input", "/seq"),
		"control": objectExpression(map[string]Expression{"kind": literalExpression("continuation_state"), "payload": readExpression("input", "/state"), "source": {Op: "capture_source", Source: expressionPointer(readExpression("input", "/state"))}}),
	})
	decode := document.Mapping.Directions[DecodeEvent]
	decode.Program = Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/kind")), Cases: map[string]Expression{"state": control}, Otherwise: expressionPointer(decode.Program)}
	document.Mapping.Directions[DecodeEvent] = decode
	encode := document.Mapping.Directions[EncodeEvent]
	encode.Program = Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/type")), Cases: map[string]Expression{string(ControlUpdated): objectExpression(map[string]Expression{
		"kind": literalExpression("state"), "response_id": readExpression("input", "/responseId"), "seq": readExpression("input", "/sequence"), "state": readExpression("input", "/control/payload"),
	})}, Otherwise: expressionPointer(encode.Program)}
	document.Mapping.Directions[EncodeEvent] = encode
	document.Mapping.ConversionDefaults = append(document.Mapping.ConversionDefaults, ProtocolConversionDefault{ID: "event-control", Order: 210, Enabled: true, Phase: ConversionEvent, Control: &ProtocolControlDisposition{Kinds: []string{"continuation_state"}, Disposition: "omit", Path: "/state", Reason: "target cannot retain continuation state"}})
	return document
}

func TestPackageControlStateReceipt(t *testing.T) {
	document := controlProtocolPackage(t)
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	input := Value{raw: strings.Repeat(" ", 2<<20) + `{ "kind":"state", "response_id":"r", "seq":19, "state":{"token":"synthetic-secret","n":9007199254740993} }`}
	frame, err := compiled.DecodeFrame(t.Context(), input, options)
	if err != nil || len(frame.Events) != 1 {
		t.Fatal(frame, err)
	}
	event := frame.Events[0]
	if event.Control == nil || event.Control.FrameNumber != 1 || event.Control.Source.Path != "/state" || event.controlReceipt == nil {
		t.Fatal("state source or actual frame ordinal was lost", event.Control)
	}
	for _, retained := range []Value{event.Control.Payload, event.controlReceipt.payload, event.controlReceipt.responseID, event.controlReceipt.sequence} {
		if sharesValueStorage(input, retained) {
			t.Fatal("small control retained the padded source frame")
		}
	}
	collector, err := NewResponseCollector(compiled.Target(EncodeEvent, Scope{}), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(event); err != nil || !collector.startedResponseID.IsZero() || len(collector.response.Metadata) != 0 {
		t.Fatal("control established a response or accumulated metadata", err)
	}
	session, err := NewSessionReplay(compiled.Target(EncodeEvent, Scope{}), compiled.Target(EncodeEvent, Scope{}), DefaultLimits(), SessionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := session.Consume(UpstreamEvent, event); err != nil || !accepted || session.isStarted || len(session.responses) != 0 {
		t.Fatal("control changed session or generation state", err)
	}
	public, err := EncodeValue(event)
	if err != nil {
		t.Fatal(err)
	}
	var forged Event
	if err := public.Decode(&forged); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Consume(UpstreamEvent, forged); err == nil {
		t.Fatal("serialized control regained source authority")
	}
	wire, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()})
	if err != nil || len(wire) != 1 || wire[0] != input {
		t.Fatal("native control bytes changed", wire, err)
	}
	nativeEvent := event
	native := *frame.native
	nativeEvent.Native = &native
	if issues := CheckEvent(nativeEvent, compiled.Target(EncodeEvent, Scope{}), DefaultLimits()); len(issues) != 0 {
		t.Fatal("original native control lost its proof", issues)
	}
	native.Source.Scope.Account = "different"
	if len(CheckEvent(nativeEvent, compiled.Target(EncodeEvent, Scope{}), DefaultLimits())) == 0 {
		t.Fatal("native digest bypassed source scope")
	}
	native = *frame.native
	document.Mapping.Directions[EncodeEvent].Program.Cases[string(ControlUpdated)].Fields["extension"] = literalExpression(true)
	changed := compilePackageForTest(t, document)
	if len(CheckEvent(nativeEvent, changed.Target(EncodeEvent, Scope{}), DefaultLimits())) == 0 {
		t.Fatal("native digest bypassed a changed target mapping")
	}
	frame.native = nil
	wire, err = compiled.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()})
	if err != nil || len(wire) != 1 {
		t.Fatal("control could not rebuild without root native", wire, err)
	}
	payload, err := readValuePointer(wire[0], []string{"state"})
	if err != nil || !equalValues(payload, event.Control.Payload) {
		t.Fatal("explicit rendering changed the state payload", err)
	}
	for index, kind := range []EventType{ResponseStarted, ResponseFinished} {
		if _, err := compiled.DecodeFrame(t.Context(), packageStreamFrame(kind, index+20, ""), options); err != nil {
			t.Fatal(err)
		}
	}
	if err := compiled.FinishInputEvents(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeEvents(t.Context(), input, EvaluationContext{}); err == nil {
		t.Fatal("single-frame mapping minted a state receipt")
	}
	mutated := event
	copy := *event.Control
	copy.Payload = Value{raw: `{"token":"different"}`}
	mutated.Control = &copy
	if len(CheckEvent(mutated, compiled.Target(EncodeEvent, Scope{}), DefaultLimits())) == 0 {
		t.Fatal("control payload modification retained source authority")
	}
}

func TestPackageControlDisposition(t *testing.T) {
	source := compilePackageForTest(t, controlProtocolPackage(t))
	frame, err := source.DecodeFrame(t.Context(), packageStreamFrame("state", 7, `,"state":{"token":"synthetic-secret"}`), EvaluationContext{State: NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	event := frame.Events[0]
	document := controlProtocolPackage(t)
	document.Spec.ID, document.Spec.Family = "control-target", "other-wire"
	target := compilePackageForTest(t, document)
	defaults := DefaultConversionPolicy(target, source)
	route := ConversionContext{Source: source.Identity(), Target: target.Identity()}
	conversion, err := ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	sink := &DiagnosticSink{}
	result, err := conversion.Event(t.Context(), event, route, sink)
	if err != nil || !isOmittedControlEvent(result, target.Target(EncodeEvent, Scope{})) || isOmittedAuxiliaryEvent(result, target.Target(EncodeEvent, Scope{})) || result.Control != nil || result.controlReceipt != nil {
		t.Fatal("state omission retained ownership or acquired auxiliary privilege", result, err)
	}
	if len(sink.Issues()) != 1 || sink.Issues()[0].Code != ConversionDegraded {
		t.Fatal("missing explicit state loss diagnostic", sink.Issues())
	}
	defaults.Mode = "strict"
	strict, err := ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Event(t.Context(), event, route, nil); err == nil {
		t.Fatal("strict conversion omitted state")
	}
	defaults.Mode = "compatible"
	for index := range defaults.Rules {
		if defaults.Rules[index].Action == "control_event" {
			defaults.Rules[index].Enabled = false
		}
	}
	disabled, err := ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	result, err = disabled.Event(t.Context(), event, route, nil)
	if err != nil || result.Control == nil || len(CheckEvent(result, target.Target(EncodeEvent, Scope{}), DefaultLimits())) == 0 {
		t.Fatal("auxiliary default bypassed a disabled state rule", err)
	}
}
