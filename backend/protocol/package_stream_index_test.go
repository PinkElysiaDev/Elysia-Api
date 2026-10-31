package protocol

import (
	"errors"
	"fmt"
	"testing"
)

func indexedEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := statefulEventPackage(t)
	mapping := document.Mapping.Directions[DecodeEvent]
	isStart := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(ItemStarted)}}
	mapping.State["nextItem"] = ProtocolStateSlot{Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(0), Update: Expression{Op: "if", When: &isStart,
		Then:      &Expression{Op: "integer", Operation: "add", Items: []Expression{readExpression("state", "/nextItem"), literalExpression(1)}},
		Otherwise: expressionPointer(readExpression("state", "/nextItem"))}}
	mapping.Indexes = map[string]ProtocolStateIndex{"items": {KeyMode: "string", Policy: "reject", Schema: ValueSchema{Type: ObjectType, Required: []string{"position"}, Properties: map[string]ValueSchema{"position": {Type: IntegerType}}}}}
	key := readExpression("input", "/item_id")
	lookup := Expression{Op: "recall", Name: "items", Policy: "reject", Source: &key}
	mapping.Program.Fields["index"] = Expression{Op: "if", When: &Expression{Op: "exists", Source: &key}, Then: &Expression{Op: "let", Name: "identity", Source: &lookup,
		Body: expressionPointer(Expression{Op: "read", From: "binding", Name: "identity", Path: "/position"})}}
	store := Expression{Op: "if", When: &isStart, Then: &Expression{Op: "remember", Name: "items", Source: &key,
		Body: expressionPointer(objectExpression(map[string]Expression{"position": readExpression("state", "/nextItem")}))}}
	body := mapping.Program
	mapping.Program = Expression{Op: "let", Name: "registered", Source: &store, Body: &body}
	document.Mapping.Directions[DecodeEvent] = mapping
	return document
}

func TestPackageStreamIndexIdentityAndRollback(t *testing.T) {
	document := indexedEventPackage(t)
	limits := DefaultLimits()
	const retainedItems = 16
	limits.StateItems = retainedItems
	document.Mapping.Limits = &limits
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	for index, frame := range []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(ItemStarted, 1, `,"item_id":"a"`),
		packageStreamFrame(ItemDelta, 2, `,"item_id":"\u0061","text":"x"`),
	} {
		events, err := compiled.DecodeEvents(t.Context(), frame, options)
		if err != nil {
			t.Fatal(err)
		}
		if index > 0 && (events[0].Index == nil || *events[0].Index != 0) {
			t.Fatal("equivalent string identities lost their first position", events)
		}
	}
	for index := 1; index < retainedItems; index++ {
		frame := packageStreamFrame(ItemStarted, index+2, fmt.Sprintf(`,"item_id":"item-%d"`, index))
		if _, err := compiled.DecodeEvents(t.Context(), frame, options); err != nil {
			t.Fatal("fixed scalar slots reduced the declared identity capacity", err)
		}
	}
	key := moduleStateKey{compiled: compiled, direction: DecodeEvent}
	before := options.State.packageStates[key]
	_, err := compiled.DecodeEvents(t.Context(), packageStreamFrame(ItemStarted, 3, `,"item_id":"b"`), options)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Code != LimitExceeded {
		t.Fatal("retained identity table escaped StateItems", err)
	}
	if options.State.packageStates[key] != before || len(before.indexes[0]) != retainedItems {
		t.Fatal("failed registration committed part of the identity table")
	}
	options.State.Release()
	if options.State.packageStateBytes != 0 || options.State.packageStateItems != 0 || len(options.State.packageStates) != 0 {
		t.Fatal("stream release retained identity entries")
	}
}

func TestPackageStreamIndexKeyModes(t *testing.T) {
	for _, test := range []struct {
		mode, left, right  string
		isEqual, isInvalid bool
	}{
		{"string", `"a"`, `"\u0061"`, true, false},
		{"raw_json", `"a"`, `"\u0061"`, false, false},
		{"integer", "0", " 0 ", true, false},
		{"integer", "0", "0.0", false, true},
		{"integer", "0", "-1", false, true},
	} {
		index := compiledPackageIndex{keyMode: test.mode}
		left, err := index.key(JSONLeaf(Value{raw: test.left}))
		if err != nil {
			t.Fatal(err)
		}
		right, err := index.key(JSONLeaf(Value{raw: test.right}))
		if (err != nil) != test.isInvalid || !test.isInvalid && (left == right) != test.isEqual {
			t.Fatal("identity equality changed", test, err)
		}
	}
}
