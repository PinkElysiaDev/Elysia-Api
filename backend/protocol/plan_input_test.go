package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestOutputPlanReadsConstructedInput(t *testing.T) {
	read := readExpression("input", "/items")
	item := readExpression("item", "")
	input := JSONObject(map[string]JSONOutput{
		"items": JSONArray([]JSONOutput{
			JSONObject(map[string]JSONOutput{"text": JSONLeaf(StringValue("first <text>")), "keep": JSONLeaf(controlValue(true))}),
			JSONObject(map[string]JSONOutput{"text": JSONLeaf(StringValue("second")), "keep": JSONLeaf(controlValue(false))}),
		}),
	})
	encoded, err := input.Encode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []Expression{
		readExpression("input", ""), readExpression("root", "/items/0/text"),
		readExpression("input", "/items/0/missing"), readExpression("input", "/items/00"),
		{Op: "read", Path: "/absent", Required: true},
		{Op: "map", Source: &read, Body: &item},
		{Op: "map", Source: &read, Body: expressionPointer(objectExpression(map[string]Expression{"content": readExpression("item", "/text")}))},
		{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))},
		{Op: "map", Source: expressionPointer(Expression{Op: "map", Source: &read, Body: &item}), Body: &item},
	} {
		compiled := compileTestOutputExpression(t, expression)
		plan, ok := compileOutputPlan(compiled)
		if !ok {
			t.Fatal("constructed input plan was not compiled", expression.Op)
		}
		for buffer := 1; buffer <= encoded.Len()+32; buffer++ {
			limits := DefaultLimits()
			limits.BufferBytes = buffer
			original := evaluation{ctx: t.Context(), input: encoded, root: encoded, budget: &evaluationBudget{limits: limits}}
			view := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: limits}}
			view.inputReader = func(pointer []string) (JSONOutput, error) { return readOutputPointer(input, pointer, nil) }
			view.rootReader = view.inputReader
			want, wantErr := compiled.evaluate(original)
			got, gotErr := plan.evaluate(view)
			if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || view.budget.steps != original.budget.steps {
				t.Fatalf("%s, buffer=%d: got %s / %v (%d steps), want %s / %v (%d steps)", expression.Op, buffer, got.raw, gotErr, view.budget.steps, want.raw, wantErr, original.budget.steps)
			}
		}
	}
}

func TestConstructedFieldReadsMatchEncodedKeys(t *testing.T) {
	for _, fields := range [][]jsonOutputField{
		{{"a", JSONLeaf(coreInteger(1))}, {"b", JSONLeaf(coreInteger(2))}, {"z", JSONLeaf(coreInteger(3))}},
		{{"z", JSONLeaf(coreInteger(3))}, {"b", JSONLeaf(coreInteger(2))}, {"a", JSONLeaf(coreInteger(1))}},
		{{"a", JSONLeaf(coreInteger(1))}, {"a", JSONLeaf(coreInteger(2))}, {"bad\xff", JSONLeaf(coreInteger(3))}, {"bad�", JSONLeaf(coreInteger(4))}},
	} {
		output := JSONOutput{node: &jsonOutputNode{object: true, fields: fields}}
		output.node.hasCanonicalKeys = fields[0].key != fields[1].key
		output.node.hasSortedFields = fields[0].key == "a"
		encoded, err := output.Encode(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"0", "a", "b", "c", "z", "zz", "bad�"} {
			want, wantErr := readValuePointer(encoded, []string{key})
			got, gotErr := readOutputPointer(output, []string{key}, nil)
			if got.node != nil || got.value != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatal("constructed lookup changed encoded key semantics", key, got, want, gotErr, wantErr)
			}
		}
	}
}

func TestOutputPlanRootObjectReadScope(t *testing.T) {
	root := Value{raw: `{"a":{"n":1},"b":2}`}
	for _, tc := range []struct {
		name       string
		expression Expression
		reads      uint8
	}{
		{"identity", readExpression("input", ""), 0},
		{"single", readExpression("input", "/a"), 1},
		{"context", objectExpression(map[string]Expression{"a": readExpression("context", "/a"), "b": readExpression("context", "/b")}), 0},
		{"root-and-input", objectExpression(map[string]Expression{"a": readExpression("input", "/a"), "b": readExpression("root", "/b"), "again": readExpression("input", "/a")}), objectReuseReads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _ := compileOutputPlan(compileTestOutputExpression(t, tc.expression))
			if plan.rootObjectReads != tc.reads {
				t.Fatalf("root reads=%d, want %d", plan.rootObjectReads, tc.reads)
			}
			objects := plan.newRootObjectReader(root)
			if (objects != nil) != (tc.reads == objectReuseReads) || plan.newRootObjectReader(Value{raw: `[]`}) != nil {
				t.Fatal("root reader eligibility changed")
			}
			if objects == nil {
				return
			}
			copied := Value{raw: strings.Clone(root.raw)}
			value, err := readValuePointerWithObjectReader(copied, []string{"a", "n"}, nil, objects)
			if err != nil || value.raw != "1" || objects.fields != nil {
				t.Fatal("equal contents reused a different root's storage", value, err)
			}
			for _, input := range []struct {
				value Value
				want  string
			}{{root, "1"}, {Value{raw: `{"a":{"n":3}}`}, "3"}, {root, "1"}} {
				value, err := readValuePointerWithObjectReader(input.value, []string{"a", "n"}, nil, objects)
				if err != nil || value.raw != input.want || objects.fields == nil {
					t.Fatal("root reader crossed storage boundaries", value, err)
				}
			}
		})
	}
}

func TestOutputPlanItemReadScope(t *testing.T) {
	fields := objectExpression(map[string]Expression{"a": readExpression("item", "/a"), "b": readExpression("item", "/b")})
	nested := Expression{Op: "map", Source: expressionPointer(readExpression("item", "/children")), Body: &fields}
	filter := Expression{Op: "filter", Source: expressionPointer(readExpression("item", "/children")), When: expressionPointer(Expression{Op: "all", Items: []Expression{readExpression("item", "/a"), readExpression("item", "/b")}})}
	for _, tc := range []struct {
		name  string
		body  Expression
		reads uint8
	}{
		{"identity", readExpression("item", ""), 0},
		{"single", readExpression("item", "/a"), 1},
		{"repeated", fields, objectReuseReads},
		{"nested-map", nested, 1},
		{"nested-filter", filter, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression := Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: &tc.body}
			plan, _ := compileOutputPlan(compileTestOutputExpression(t, expression))
			if plan.itemObjectReads != 0 || plan.body.itemObjectReads != tc.reads {
				t.Fatalf("item read scope changed: outer=%d body=%d, want 0/%d", plan.itemObjectReads, plan.body.itemObjectReads, tc.reads)
			}
		})
	}
}

func TestItemObjectReaderReleasesOwnersAfterFailure(t *testing.T) {
	input := Value{raw: `{"value":1}`}
	reader := newPlanObjectReader(input, objectReuseReads)
	reader.parent = newPlanObjectReader(Value{raw: `{"parent":true}`}, objectReuseReads)
	if _, err := reader.readOwnedMember(input, "value"); err != nil {
		t.Fatal(err)
	}
	reader.releaseItem(fmt.Errorf("mapping rejected value"))
	if reader.fields != nil || reader.parent != nil || reader.root != (immutableSpan{}) {
		t.Fatal("failed item retained its input or enclosing owner")
	}
}

func TestObjectReaderBoundsDescendantsToActiveOwner(t *testing.T) {
	var entries []string
	for index := range objectReaderChildren + 3 {
		entries = append(entries, fmt.Sprintf(`"field%d":{"n":%d}`, index, index))
	}
	input := Value{raw: `{` + strings.Join(entries, ",") + `}`}
	reader := newPlanObjectReader(input, objectReuseReads)
	for range 2 {
		for index := range objectReaderChildren + 3 {
			path := []string{fmt.Sprintf("field%d", index), "n"}
			want, wantErr := readValuePointer(input, path)
			got, gotErr := readValuePointerWithObjectReader(input, path, nil, reader)
			if wantErr != nil || gotErr != nil || want != got {
				t.Fatal("bounded descendant reads changed their result", path, want, got, wantErr, gotErr)
			}
		}
	}
	if len(reader.children) != objectReaderChildren {
		t.Fatal("descendant cache did not stay bounded", len(reader.children))
	}
	reader.releaseItem(nil)
	if reader.children != nil || reader.fields != nil || reader.parent != nil || reader.root != (immutableSpan{}) {
		t.Fatal("finished item retained its descendants")
	}
	input = Value{raw: `{"nested":{"n":1}}`}
	reader = newPlanObjectReader(input, objectReuseReads)
	_, err := readValuePointerWithObjectReader(input, []string{"nested", "n"}, nil, reader)
	if err != nil || len(reader.children) != 1 {
		t.Fatal("descendant was not cached", err)
	}
	reader.releaseItem(fmt.Errorf("mapping rejected descendant"))
	if reader.fields != nil || reader.children != nil || reader.root != (immutableSpan{}) {
		t.Fatal("failed item retained its descendants")
	}
}
