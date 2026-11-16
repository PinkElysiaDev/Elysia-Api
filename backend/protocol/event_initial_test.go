package protocol

import (
	"strings"
	"testing"
)

func initialEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textVerificationDefinition(t, "initial-events")
	hasEvents := Expression{Op: "integer", Operation: "gt", Items: []Expression{
		{Op: "length", Source: expressionPointer(readExpression("input", "/events"))}, literalExpression(0),
	}}
	isInitialized := readExpression("state", "/initialized")
	initial := literalExpression(map[string]any{"schemaVersion": ProtocolCoreVersion, "type": "item.started", "itemId": "text", "item": map[string]any{"kind": "text", "payload": ""}})
	program := Expression{Op: "if", When: &hasEvents, Then: &Expression{Op: "if", When: &isInitialized,
		Then: expressionPointer(readExpression("input", "/events")), Otherwise: &Expression{Op: "array", Items: []Expression{initial, readExpression("input", "/events/0")}},
	}, Otherwise: &Expression{Op: "array"}}
	document.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Document: testDocument(DecodeEvent), Program: program, IsEventBatch: true, CanDeferEvents: true,
		State: map[string]ProtocolStateSlot{"initialized": {Schema: ValueSchema{Type: BooleanType}, Initial: literalExpression(false).Value,
			Update: Expression{Op: "any", Items: []Expression{isInitialized, hasEvents}}}},
	}
	return document
}

func TestDeclaredInitialEventsAreOncePerStreamAndRevision(t *testing.T) {
	document := initialEventPackage(t)
	compiled := compileTestDefinition(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	empty := parseTestValue(t, `{"events":[]}`)
	if frame, err := compiled.DecodeFrame(t.Context(), empty, options); err != nil || len(frame.Events) != 0 { t.Fatal("empty frame initialized a content item", frame, err) }
	delta := parseTestValue(t, `{"events":[{"schemaVersion":2,"type":"item.delta","itemId":"text","delta":"hello"}]}`)
	for index, count := range []int{2, 1} {
		frame, err := compiled.DecodeFrame(t.Context(), delta, options)
		if err != nil || len(frame.Events) != count || frame.Events[count-1].Type != ItemDelta { t.Fatal(index, frame, err) }
	}
	for _, fresh := range []struct { compiled *Compiled; options EvaluationContext }{
		{compiled, EvaluationContext{State: NewEvaluationState()}}, {compileTestDefinition(t, document), options},
	} {
		frame, err := fresh.compiled.DecodeFrame(t.Context(), delta, fresh.options)
		if err != nil || len(frame.Events) != 2 { t.Fatal("initialization leaked across stream or revision", frame, err) }
	}
	if _, err := compiled.DecodeFrame(t.Context(), delta, EvaluationContext{}); err == nil { t.Fatal("stateful mapping accepted no owner") }
}

func TestInitialEventsRejectLegacyExecutionKey(t *testing.T) {
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	raw := fixtureValue(t, initialEventPackage(t)).JSONString()
	raw = strings.Replace(raw, `"program":`, `"initial":{"op":"literal","value":[]},"program":`, 1)
	if compiled, issues := compiler.Compile([]byte(raw)); compiled != nil || IssuesError(issues) == nil { t.Fatal("legacy initial execution key accepted") }
}
