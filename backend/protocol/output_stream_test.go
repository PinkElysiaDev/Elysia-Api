package protocol

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func outputPositionPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	mapping := document.Mapping.Directions[EncodeEvent]
	for _, field := range []struct{ wire, input, runtime string }{{"response_id", "/responseId", "/stream/responseId"}, {"seq", "/sequence", "/stream/sequence"}} {
		input := readExpression("input", field.input)
		fallback := readExpression("runtime", field.runtime)
		if field.wire == "seq" {
			fallback = Expression{Op: "integer", Operation: "add", Items: []Expression{fallback, literalExpression(1)}}
		}
		mapping.Program.Fields[field.wire] = Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &input}), Then: &input, Otherwise: &fallback}
	}
	mapping.Program.Fields["observed"] = objectExpression(map[string]Expression{
		"id": readExpression("runtime", "/stream/responseId"), "sequence": readExpression("runtime", "/stream/sequence"),
		"started": readExpression("runtime", "/stream/started"), "terminal": readExpression("runtime", "/stream/terminal"),
	})
	mapping.Effects = []ProtocolSemanticEffect{{ID: "kind", Kind: "emit", Path: "/type", Target: "/kind"}}
	document.Mapping.Directions[EncodeEvent] = mapping
	return document
}

func TestOutputStreamReadsDeliveredWirePosition(t *testing.T) {
	compiled := compilePackageForTest(t, outputPositionPackage(t))
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{}).WithOutputStream(validation)
	start := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("mapped"), Sequence: fixtureValue(t, 0)}
	wire, err := compiled.EncodeEvent(t.Context(), start, options)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := wire.ReadObject()
	observed, _ := fields["observed"].ReadObject()
	if !observed["id"].IsZero() || !observed["sequence"].IsZero() || observed["started"].raw != "false" || observed["terminal"].raw != "false" {
		t.Fatal("new bound stream invented a delivered position", observed)
	}
	// The final wire, after an authored edit, owns the next visible position.
	fields["response_id"], fields["seq"] = StringValue("delivered"), fixtureValue(t, 41)
	wire = fixtureValue(t, fields)
	if err := validation.Consume(t.Context(), wire); err != nil {
		t.Fatal(err)
	}
	if validation.packageStream.delivered.hasStarted {
		t.Fatal("validation was mistaken for successful delivery")
	}
	// Explicitly simulate a successful transport flush for this unit fixture.
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	failure := Event{SchemaVersion: ProtocolCoreVersion, Type: OperationFailed, Error: StringValue("synthetic failure")}
	wire, err = compiled.EncodeEvent(t.Context(), failure, options)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ = wire.ReadObject()
	if !fields["response_id"].IsString("delivered") || fields["seq"].raw != "42" {
		t.Fatal("local failure did not follow the actual delivered wire", wire)
	}
	if err := validation.Consume(t.Context(), wire); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	if !validation.packageStream.delivered.isTerminal || validation.packageStream.delivered.sequence != 42 {
		t.Fatal("delivered terminal was lost")
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputStreamAbortPreservesOnlyCommittedFacts(t *testing.T) {
	compiled := compilePackageForTest(t, outputPositionPackage(t))
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{}).WithOutputStream(validation)
	start := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("delivered"), Sequence: fixtureValue(t, 0)}
	wire, err := compiled.EncodeEvent(t.Context(), start, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), wire); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	committed := validation.packageStream.delivered
	failed := Event{SchemaVersion: ProtocolCoreVersion, Type: OperationFailed, Error: StringValue("synthetic")}
	wire, err = compiled.EncodeEvent(t.Context(), failed, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), wire); err != nil {
		t.Fatal(err)
	}
	validation.AbortDelivery()
	if validation.packageStream.delivered != committed || validation.packageStream.hasPendingDelivery || validation.CommitDelivery() == nil || validation.Consume(t.Context(), wire) == nil || validation.Finish() == nil {
		t.Fatal("abort rewrote committed facts or retained staged delivery")
	}
}

func TestOutputStreamBindingAndLazyReads(t *testing.T) {
	document := outputPositionPackage(t)
	compiled := compilePackageForTest(t, document)
	scope := Scope{Provider: "fixture", Account: "account", Model: "m", Session: "s"}
	validation, _ := compiled.NewWireStreamValidation(scope)
	otherDocument := outputPositionPackage(t)
	otherDocument.Spec.Version = "other"
	other := compilePackageForTest(t, otherDocument)
	otherValidation, _ := other.NewWireStreamValidation(scope)
	sourceValidation, _ := compiled.NewWireInputStreamValidation(scope)
	changedMapping := *compiled
	changedMapping.mappings = maps.Clone(compiled.mappings)
	changedEntry := changedMapping.mappings[EncodeEvent]
	changedEntry.definitionHash = "different-encoder"
	changedMapping.mappings[EncodeEvent] = changedEntry
	changedValidation := *validation
	changedValidation.compiled = &changedMapping
	start := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 0)}
	for _, fixture := range []struct {
		name    string
		options EvaluationContext
	}{
		{"missing", EvaluationContext{Scope: scope}},
		{"scope", (EvaluationContext{Scope: Scope{Provider: "fixture", Account: "other", Model: "m", Session: "s"}}).WithOutputStream(validation)},
		{"identity", (EvaluationContext{Scope: scope}).WithOutputStream(otherValidation)},
		{"mapping", (EvaluationContext{Scope: scope}).WithOutputStream(&changedValidation)},
		{"direction", (EvaluationContext{Scope: scope}).WithOutputStream(sourceValidation)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if _, err := compiled.EncodeEvent(t.Context(), start, fixture.options); err == nil || !strings.Contains(err.Error(), "not bound to this mapping and scope") {
				t.Fatal("unbound metadata was presented as an unstarted stream", err)
			}
		})
	}
	// Identical packages compiled separately may bind; the complete content
	// identity, not a particular Go pointer, defines this mapping.
	identical := compilePackageForTest(t, document)
	if _, err := identical.EncodeEvent(t.Context(), start, (EvaluationContext{Scope: scope}).WithOutputStream(validation)); err != nil {
		t.Fatal(err)
	}
	delete(document.Mapping.Directions[EncodeEvent].Program.Fields, "observed")
	unused := compilePackageForTest(t, document)
	if _, err := unused.EncodeEvent(t.Context(), start, (EvaluationContext{Scope: scope}).WithOutputStream(otherValidation)); err != nil {
		t.Fatal("unused conditional runtime branch rejected inherited options", err)
	}
	sourceOptions := (EvaluationContext{Scope: scope, State: NewEvaluationState()}).WithOutputStream(validation)
	defer sourceOptions.State.Release()
	if _, err := unused.DecodeFrame(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), sourceOptions); err != nil {
		t.Fatal("source decoding rejected an unused output binding", err)
	}
	if _, err := unused.DecodeFrame(t.Context(), packageStreamFrame(ResponseFinished, 1, ""), sourceOptions); err != nil {
		t.Fatal(err)
	}
	if err := unused.FinishInputEvents(t.Context(), sourceOptions); err != nil {
		t.Fatal(err)
	}
}

func TestOutputStreamValidationFailureDoesNotPublishPosition(t *testing.T) {
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	for _, raw := range []string{
		`{"kind":17,"response_id":"r","seq":1}`,
		`{"kind":"response.finished","response_id":"r","seq":0}`,
	} {
		validation, _ := compiled.NewWireStreamValidation(Scope{})
		if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
			t.Fatal(err)
		}
		before := validation.packageStream.accepted
		if err := validation.Consume(t.Context(), Value{raw: raw}); err == nil {
			t.Fatal("invalid final wire accepted")
		}
		if validation.packageStream.accepted != before || validation.packageStream.delivered != (streamPosition{}) || validation.CommitDelivery() == nil {
			t.Fatal("failed validation published or committed a stale position")
		}
	}
}

func TestOutputStreamNativePositionOwnsOnlyIdentity(t *testing.T) {
	document := outputPositionPackage(t)
	delete(document.Mapping.Directions[EncodeEvent].Program.Fields, "observed")
	compiled := compilePackageForTest(t, document)
	validation, _ := compiled.NewWireStreamValidation(Scope{})
	options := (EvaluationContext{State: NewEvaluationState()}).WithOutputStream(validation)
	defer options.State.Release()
	wire := Value{raw: `{"kind":"response.started","response_id":"small-id","seq":7,"padding":"` + strings.Repeat("x", 2<<20) + `"}`}
	decoded, err := compiled.DecodeFrame(t.Context(), wire, options)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := compiled.EncodeConvertedFrame(t.Context(), decoded, decoded.Events, options)
	if err != nil || len(frames) != 1 || frames[0] != wire {
		t.Fatal("unchanged native frame was mapped instead of replayed", err)
	}
	if err := validation.Consume(t.Context(), frames[0]); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	position := validation.packageStream.delivered
	if position.responseID != "small-id" || position.sequence != 7 || sharesValueStorage(wire, Value{raw: position.responseID}) {
		t.Fatal("native delivery lost its identity or retained the large source frame")
	}
	if _, err := compiled.DecodeFrame(t.Context(), Value{raw: `{"kind":"response.finished","response_id":"small-id","seq":8}`}, options); err != nil {
		t.Fatal(err)
	}
	if err := compiled.FinishInputEvents(t.Context(), options); err != nil {
		t.Fatal(err)
	}
}

func TestOutputStreamRuntimeReadContracts(t *testing.T) {
	for _, fixture := range []struct {
		direction Direction
		path      string
	}{{DecodeEvent, "/stream/started"}, {EncodeEvent, "/stream"}, {EncodeEvent, "/stream/private"}} {
		document := eventProtocolPackage(t)
		document.Mapping.Directions[fixture.direction].Program.Fields["private"] = readExpression("runtime", fixture.path)
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		if compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); compiled != nil || IssuesError(issues) == nil {
			t.Fatal("undeclared or non-output stream metadata compiled", fixture, issues)
		}
	}
	document := outputPositionPackage(t)
	required := readExpression("runtime", "/stream/responseId")
	required.Required = true
	document.Mapping.Directions[EncodeEvent].Program.Fields["required_id"] = required
	compiled := compilePackageForTest(t, document)
	validation, _ := compiled.NewWireStreamValidation(Scope{})
	start := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 0)}
	if _, err := compiled.EncodeEvent(t.Context(), start, (EvaluationContext{}).WithOutputStream(validation)); err == nil || !strings.Contains(err.Error(), "required field is absent") {
		t.Fatal("required runtime read silently omitted an undelivered ID", err)
	}
}

func TestOutputStreamSequenceVerificationSimulatesDelivery(t *testing.T) {
	compiled := compilePackageForTest(t, outputPositionPackage(t))
	input := []Event{
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 0)},
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished},
	}
	result, err := executeEventFixture(t.Context(), compiled, Sample{Direction: EncodeEvent, Sequence: true, Input: fixtureValue(t, input)})
	if err != nil {
		t.Fatal("explicitly simulated delivery failed", err)
	}
	var frames []Value
	if err := result.output.Decode(&frames); err != nil || len(frames) != 2 {
		t.Fatal("unexpected verified output", result.output, err)
	}
	fields, _ := frames[1].ReadObject()
	if !fields["response_id"].IsString("r") || fields["seq"].raw != "1" {
		t.Fatal("offline sequence did not use its simulated delivered position", frames[1])
	}
}

func TestOutputStreamNativeCannotHideStateDependentEncoding(t *testing.T) {
	document := outputPositionPackage(t)
	document.Mapping.Directions[EncodeEvent].Program.Fields["seq"] = Expression{Op: "integer", Operation: "add", Items: []Expression{
		readExpression("runtime", "/stream/sequence"), literalExpression(1),
	}}
	compiled := compilePackageForTest(t, document)
	validation, _ := compiled.NewWireStreamValidation(Scope{})
	sourceValidation, _ := compiled.NewWireInputStreamValidation(Scope{})
	sourceOptions := EvaluationContext{State: NewEvaluationState()}
	defer sourceOptions.State.Release()
	// Source order is valid (1, 2). Only the first delivered target frame was
	// edited to 41; a state-dependent encoder must not replay the next source 2.
	if _, err := sourceValidation.DecodeFrame(t.Context(), packageStreamFrame(ResponseStarted, 1, ""), sourceOptions); err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 41, "")); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	wire := packageStreamFrame(UsageUpdated, 2, `,"usage":{"input":7},"future":{"n":9007199254740993}`)
	frame, err := sourceValidation.DecodeFrame(t.Context(), wire, sourceOptions)
	if err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{}).WithOutputStream(validation)
	events, err := compiled.DecodeEvents(t.Context(), wire, EvaluationContext{})
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	semantic := events[0]
	semantic.Native = nil
	mapped, err := compiled.EncodeEvent(t.Context(), semantic, options)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := mapped.ReadObject()
	if fields["seq"].raw != "42" {
		t.Fatal("semantic encoding did not read the delivered position", mapped)
	}
	for _, entry := range []string{"event", "frame", "converted-frame"} {
		t.Run(entry, func(t *testing.T) {
			var encodeErr error
			var output Value
			if entry == "event" {
				output, encodeErr = compiled.EncodeEvent(t.Context(), events[0], options)
			} else {
				var encoded []Value
				if entry == "frame" {
					encoded, encodeErr = compiled.EncodeFrame(t.Context(), frame, options)
				} else {
					encoded, encodeErr = compiled.EncodeConvertedFrame(t.Context(), frame, frame.Events, options)
				}
				if len(encoded) > 0 {
					output = encoded[0]
				}
			}
			var failure *ConversionError
			if !errors.As(encodeErr, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != UnsupportedNative {
				t.Fatal("state-dependent native encoding must reject until dynamic field ownership is proven; it cannot restore sequence 2 or discard unknown fields", encodeErr, output.JSONString())
			}
		})
	}
	if _, err := sourceValidation.DecodeFrame(t.Context(), packageStreamFrame(ResponseFinished, 3, ""), sourceOptions); err != nil {
		t.Fatal(err)
	}
	if err := sourceValidation.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputStreamNativeKeepsOriginalProvenanceAndBudgetErrors(t *testing.T) {
	document := outputPositionPackage(t)
	limits := DefaultLimits()
	limits.BufferBytes = 64 << 10
	document.Mapping.Limits = &limits
	compiled := compilePackageForTest(t, document)
	validation, _ := compiled.NewWireStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 41, "")); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	options := (EvaluationContext{}).WithOutputStream(validation)
	events, err := compiled.DecodeEvents(t.Context(), packageStreamFrame(UsageUpdated, 2, `,"usage":{"input":7}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "budget"} {
		t.Run(fault, func(t *testing.T) {
			event := events[0]
			native := *event.Native
			event.Native = &native
			if fault == "scope" {
				native.Source.Scope.Account = "other-account"
			} else {
				native.Value = Value{raw: `{"padding":"` + strings.Repeat("x", 128<<10) + `"}`}
			}
			_, expected := ReplayNative(native, compiled.target(EncodeEvent, options), nil, compiled.wireLimits(t.Context(), EncodeEvent))
			_, actual := compiled.EncodeEvent(t.Context(), event, options)
			var want, got *ConversionError
			if !errors.As(expected, &want) || !errors.As(actual, &got) || len(got.Issues) != 1 || got.Issues[0].Code != want.Issues[0].Code {
				t.Fatal("unsupported stateful encoding masked the original native failure", expected, actual)
			}
			var wantResource, gotResource *ResourceLimitError
			if errors.As(expected, &wantResource) && (!errors.As(actual, &gotResource) || !reflect.DeepEqual(wantResource, gotResource)) {
				t.Fatal("native resource cause changed", wantResource, gotResource)
			}
		})
	}
}
