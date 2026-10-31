package protocol

import "testing"

func auxiliaryProtocolPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	document.Mapping.Capabilities[NativeExtensionsCapability] = true
	document.Spec.Documents["provider-event"].Stream.Events["auxiliary"] = ProtocolStreamEventSpec{{Action: "observe", FrameClass: "auxiliary"}}
	document.Spec.Documents["provider-event"].Stream.Events["heartbeat"] = ProtocolStreamEventSpec{{Action: "control", FrameClass: "control", AllowBeforeStart: true, AfterTerminal: true}}
	document.Mapping.Directions[DecodeEvent].Program.Fields["type"] = Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/kind")), Cases: map[string]Expression{"auxiliary": literalExpression(NativeEvent)}, Otherwise: expressionPointer(readExpression("input", "/kind"))}
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "event-metadata", Order: 220, Enabled: true, Phase: ConversionEvent, Auxiliary: &ProtocolAuxiliaryDisposition{Disposition: "omit", Path: "/metadata", Reason: "target cannot represent this auxiliary event"}}}
	return document
}

func decodeAuxiliaryFixture(t *testing.T, compiled *Compiled) (Event, *EventFrame) {
	t.Helper()
	options := EvaluationContext{State: NewEvaluationState()}
	if _, err := compiled.DecodeFrame(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), options); err != nil {
		t.Fatal(err)
	}
	frame, err := compiled.DecodeFrame(t.Context(), Value{raw: `{ "kind":"auxiliary", "response_id":"r", "seq":1, "opaque":{"x":9007199254740993} }`}, options)
	if err != nil || len(frame.Events) != 1 {
		t.Fatal(frame, err)
	}
	return frame.Events[0], frame
}

func TestPackageSourceAuxiliaryReceipt(t *testing.T) {
	compiled := compilePackageForTest(t, auxiliaryProtocolPackage(t))
	options := EvaluationContext{State: NewEvaluationState()}
	control, err := compiled.DecodeFrame(t.Context(), packageStreamFrame("heartbeat", 0, ""), options)
	if err != nil || len(control.Events) != 0 || control.native != nil {
		t.Fatal("control became generation or native replay", control, err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), packageStreamFrame("auxiliary", 1, ""), options); err == nil {
		t.Fatal("auxiliary frame established a response")
	}
	event, frame := decodeAuxiliaryFixture(t, compiled)
	collector, err := NewResponseCollector(compiled.Target(EncodeEvent, Scope{}), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(event); err == nil {
		t.Fatal("collector accepted auxiliary before its own response start")
	}
	collector, _ = NewResponseCollector(compiled.Target(EncodeEvent, Scope{}), DefaultLimits())
	start := Event{SchemaVersion: ProtocolCoreVersion, Source: compiled.Identity(), Type: ResponseStarted, ResponseID: StringValue("r")}
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(event); err != nil {
		t.Fatal(err)
	}
	if len(collector.response.Metadata) != 0 || len(collector.response.Content) != 0 {
		t.Fatal("collector accumulated an opaque auxiliary frame")
	}
	plain, err := compiled.DecodeEvents(t.Context(), frame.native.Value, EvaluationContext{})
	if err != nil || plain[0].auxiliary != nil {
		t.Fatal("single-frame API minted source lifecycle authority", err)
	}
	collector, _ = NewResponseCollector(compiled.Target(EncodeEvent, Scope{}), DefaultLimits())
	if _, _, err := collector.Consume(start); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collector.Consume(plain[0]); err == nil {
		t.Fatal("unvalidated native event received auxiliary collection privileges")
	}
	options.State = NewEvaluationState()
	if _, err := compiled.DecodeFrame(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), options); err != nil {
		t.Fatal(err)
	}
	if err := compiled.FinishInputEvents(t.Context(), options); err == nil {
		t.Fatal("direct frame API accepted early EOF")
	}
	options.State.Release()
	if len(options.State.sourceStreams) != 0 {
		t.Fatal("release retained source validators")
	}
	options.State = NewEvaluationState()
	for index, kind := range []EventType{ResponseStarted, ResponseFinished} {
		if _, err := compiled.DecodeFrame(t.Context(), packageStreamFrame(kind, index, ""), options); err != nil {
			t.Fatal(err)
		}
	}
	if err := compiled.FinishInputEvents(t.Context(), options); err != nil {
		t.Fatal(err)
	}
}

func TestPackageAuxiliaryDisposition(t *testing.T) {
	source := compilePackageForTest(t, auxiliaryProtocolPackage(t))
	event, frame := decodeAuxiliaryFixture(t, source)
	same, err := ResolveConversion(DefaultConversionPolicy(source, source))
	if err != nil {
		t.Fatal(err)
	}
	result, err := same.Event(t.Context(), event, ConversionContext{Source: source.Identity(), Target: source.Identity()}, nil)
	if err != nil || result.Native == nil || result.Native.Value != frame.native.Value {
		t.Fatal("same-wire bytes changed", err)
	}
	document := auxiliaryProtocolPackage(t)
	document.Spec.ID, document.Spec.Family = "other-target", "other-wire"
	target := compilePackageForTest(t, document)
	route := ConversionContext{Source: source.Identity(), Target: target.Identity()}
	defaults := DefaultConversionPolicy(target, source)
	conversion, err := ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	result, err = conversion.Event(t.Context(), event, route, nil)
	if err != nil || !isOmittedAuxiliaryEvent(result, target.Target(EncodeEvent, Scope{})) || result.Native != nil || result.auxiliary != nil {
		t.Fatal("omission retained payload or lacked target authority", result, err)
	}
	if event.Native == nil || event.auxiliary == nil {
		t.Fatal("projection changed source ownership")
	}
	document.Mapping.Directions[EncodeEvent].Program.Fields["extension"] = literalExpression(true)
	changed := compilePackageForTest(t, document)
	if changed.Identity() != target.Identity() || isOmittedAuxiliaryEvent(result, changed.Target(EncodeEvent, Scope{})) {
		t.Fatal("authored version alone authorized omission under a different mapping")
	}
	defaults.Mode = "strict"
	strict, _ := ResolveConversion(defaults)
	if _, err := strict.Event(t.Context(), event, route, nil); err == nil {
		t.Fatal("strict policy omitted auxiliary data")
	}
	defaults.Mode, defaults.Rules[0].Enabled = "compatible", false
	disabled, _ := ResolveConversion(defaults)
	result, err = disabled.Event(t.Context(), event, route, nil)
	if err != nil || len(CheckEvent(result, target.Target(EncodeEvent, Scope{}), DefaultLimits())) == 0 {
		t.Fatal("disabled rule silently omitted an event", err)
	}
	defaults.Rules = append(defaults.Rules, ConversionRule{ID: "strip-native", Order: 221, Enabled: true, Phase: ConversionEvent, Action: "remove", Path: "/native"})
	stripping, err := ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stripping.Event(t.Context(), event, route, nil); err == nil {
		t.Fatal("JSON rule erased private native protection")
	}
	mutated := event
	mutated.Delta = StringValue("generation")
	if _, err := conversion.Event(t.Context(), mutated, route, nil); err == nil {
		t.Fatal("mixed generation data was omitted")
	}
}
