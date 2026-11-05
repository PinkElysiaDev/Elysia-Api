package protocol

import (
	"errors"
	"reflect"
	"testing"
)

func TestVerificationBindsStableOperationMetadata(t *testing.T) {
	document := eventProtocolPackage(t)
	encoder := document.Mapping.Directions[EncodeEvent].Program.Fields
	encoder["execution"] = readExpression("runtime", "/operation/id")
	encoder["started_at"] = readExpression("runtime", "/operation/startedAt")
	compiled := compilePackageForTest(t, document)
	input := fixtureValue(t, []Event{
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: coreInteger(0)},
		{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r"), Sequence: coreInteger(1)},
	})
	sample := Sample{ID: "operation-context", Direction: EncodeEvent, Sequence: true, Input: input, Context: Object{"operation": Value{raw: `{"id":"untrusted","startedAt":999}`}}}
	result, err := executeVerificationSample(t.Context(), compiled, sample)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := readArray(result.output)
	if err != nil || len(frames) != 2 {
		t.Fatal(frames, err)
	}
	for _, frame := range frames {
		id, err := readValuePointer(frame, []string{"execution"})
		if err != nil || !id.IsString(verificationOperationID) {
			t.Fatal("context JSON replaced the operation identity", id, err)
		}
		startedAt, err := readValuePointer(frame, []string{"started_at"})
		if err != nil || startedAt != coreInteger(verificationStartedAt) {
			t.Fatal("frames did not share the offline start time", startedAt, err)
		}
	}
	if err := verifyEventRoundTrip(t.Context(), compiled, sample, result); err != nil {
		t.Fatal(err)
	}
}

func TestCoreControlsUseBoundOperationMetadata(t *testing.T) {
	document := eventProtocolPackage(t)
	operationID := readExpression("runtime", "/operation/id")
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{
		{ID: "response-identity", Order: 230, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{
			When: expressionPointer(Expression{Op: "not", Source: &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/id"))}}),
			Set:  map[string]Expression{"/id": operationID, "/attributes/created_at": readExpression("runtime", "/operation/startedAt")},
		}}},
		{ID: "event-identity", Order: 230, Enabled: true, Phase: ConversionEvent, Steps: []ProtocolControlStep{{Each: &ProtocolControlEach{
			Select: "node", Steps: []ProtocolControlStep{{Set: map[string]Expression{"/id": {Op: "join", Items: []Expression{operationID, literalExpression(":"), readExpression("root", "/itemId")}}}}},
		}}}},
	}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	spoofed := Object{"operation": parseTestValue(t, `{"id":"untrusted","startedAt":999}`)}
	options, err := (EvaluationContext{Values: spoofed}).WithOperation("bound-execution", 123)
	if err != nil {
		t.Fatal(err)
	}
	route := (ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Operation: "untrusted"}).WithOperation(options)
	response := &Response{SchemaVersion: ProtocolCoreVersion}
	result, err := conversion.Response(t.Context(), response, route, nil)
	if err != nil || !result.ID.IsString("bound-execution") || result.Attributes["created_at"] != coreInteger(123) || !response.ID.IsZero() {
		t.Fatal("response controls did not share execution facts", result, err)
	}
	for _, id := range []Value{StringValue("provider-response"), coreInteger(7)} {
		response.ID = id
		result, err = conversion.Response(t.Context(), response, route, nil)
		if err != nil || result.ID != id {
			t.Fatal("identity assignment treated a present value as missing", result, err)
		}
	}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ItemStarted, ItemID: StringValue("semantic-item"), Item: &Node{Kind: MessageNode, Role: StringValue("assistant")}}
	projected, err := conversion.Event(t.Context(), event, route, nil)
	if err != nil || !projected.Item.ID.IsString("bound-execution:semantic-item") || !event.Item.ID.IsZero() {
		t.Fatal("node selection lost its event runtime or root", projected, err)
	}
	response.ID = Value{}
	unbound := route.WithOperation(EvaluationContext{Values: spoofed})
	_, err = conversion.Response(t.Context(), response, unbound, nil)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Path != "/operation" {
		t.Fatal("unbound controls accepted user context as execution facts", err)
	}
}

func TestCoreControlsLimitOperationReadPermissions(t *testing.T) {
	for _, fixture := range []struct {
		root reflect.Type
		path string
	}{
		{reflect.TypeFor[Request](), "/operation/id"},
		{reflect.TypeFor[Response](), "/interleavedThinking"},
		{reflect.TypeFor[Response](), "/scope/model"},
		{reflect.TypeFor[Event](), "/frame/usage"},
	} {
		// Reading the same named program in an allowed scope first must not
		// grant another control phase or runtime family those permissions.
		compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{"runtime-read": readExpression("runtime", fixture.path)})
		if _, err := compiler.compilePlan(Expression{Op: "ref", Ref: "runtime-read"}, "/allowed", expressionScope{coreInput: fixture.root, canReadRuntime: true, canReadFrameUsage: true}, 1); err != nil {
			t.Fatal(err)
		}
		steps := []ProtocolControlStep{{Diagnostic: &ProtocolControlDiagnostic{Kind: "normalize", Path: "", Reason: Expression{Op: "stringify_json", Source: &Expression{Op: "ref", Ref: "runtime-read"}}}}}
		if _, err := compileConversionControlSteps(steps, compiler, "/controls", fixture.root, false); err == nil {
			t.Fatal("operation permission exposed another runtime field or request phase", fixture.root, fixture.path)
		}
	}
}
