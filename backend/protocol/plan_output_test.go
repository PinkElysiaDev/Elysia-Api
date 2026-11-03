package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func compileTestOutputExpression(t testing.TB, expression Expression) *compiledExpression {
	t.Helper()
	compiler := expressionCompiler{limits: DefaultLimits(), references: map[string]Expression{"identity": readExpression("input", "")}, resolving: map[string]bool{}, used: map[string]bool{}}
	compiled, err := compiler.compile(expression, "/mapping", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestOutputPlanParity(t *testing.T) {
	read := readExpression("input", "")
	item := readExpression("item", "")
	text := readExpression("item", "/text")
	object := objectExpression(map[string]Expression{"text": text, "missing": readExpression("item", "/missing")})
	mapObjects := Expression{Op: "map", Source: &read, Body: &object}
	mapItems := Expression{Op: "map", Source: &item, Body: &object}
	nestedItems := Expression{Op: "map", Source: expressionPointer(readExpression("item", "/children")), Body: expressionPointer(objectExpression(map[string]Expression{
		"first": readExpression("item", "/text"), "second": readExpression("item", "/text"), "root": readExpression("root", "/label"),
	}))}
	nestedObjects := objectExpression(map[string]Expression{"a_before": text, "b_children": nestedItems, "c_after": text})
	for _, tc := range []struct {
		name       string
		expression Expression
		input      string
	}{
		{"identity", read, ` { "n" : 9007199254740993, "n":1e+3 } `},
		{"reference", Expression{Op: "ref", Ref: "identity"}, ` [ 1, null ] `},
		{"literal", literalExpression("<>&\u2028"), `null`},
		{"omit", Expression{Op: "omit"}, `null`},
		{"empty-object", Expression{Op: "object"}, `null`},
		{"empty-array", Expression{Op: "array"}, `null`},
		{"nested", objectExpression(map[string]Expression{"z": {Op: "array", Items: []Expression{read, {Op: "omit"}, literalExpression(nil)}}, "a<&": {Op: "object", Fields: map[string]Expression{"n": readExpression("input", "/n")}}}), `{"n":9007199254740993,"text":"<>& ","dupe":1,"dupe":2}`},
		{"invalid-key-encoding", objectExpression(map[string]Expression{"bad\xff": read}), `"text"`},
		{"map", mapObjects, `[{"text":"<>& "},{"text":null},{}]`},
		{"map-raw", Expression{Op: "map", Source: &read, Body: &item}, `[ {"k":"<"}, [ 1, 2 ], null ]`},
		{"map-absent", mapObjects, `[{},{}]`},
		{"map-nested-item-scopes", Expression{Op: "map", Source: expressionPointer(readExpression("input", "/items")), Body: &nestedObjects}, `{"label":"root","items":[{"text":"outer-one","children":[{"text":"inner-one"},{"text":"inner-two"}]},{"text":"outer-two","children":[{}, {"text":"inner-three"}]}]}`},
		{"map-null", mapObjects, `null`},
		{"map-scalar", mapObjects, `false`},
		{"flatmap-raw", Expression{Op: "flatmap", Source: &read, Body: &item}, `[[ {"x":"<"} ],[],[1e+3,null]]`},
		{"flatmap-planned", Expression{Op: "flatmap", Source: &read, Body: &mapItems}, `[[{"text":"<"},{"text":"two"}],[],[{"text":"three"}]]`},
		{"flatmap-invalid", Expression{Op: "flatmap", Source: &read, Body: &item}, `[[1],true]`},
		{"flatmap-object-scopes", Expression{Op: "flatmap", Source: &read, Body: expressionPointer(Expression{Op: "array", Items: []Expression{object}})}, `[{"text":"first"},{"text":"second","missing":null},{}]`},
		{"if-object", Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/present"))}), Then: expressionPointer(objectExpression(map[string]Expression{"value": readExpression("input", "/present")}))}, `{"present":false}`},
		{"if-omit", Expression{Op: "if", When: expressionPointer(readExpression("input", "/flag")), Then: expressionPointer(Expression{Op: "array", Items: []Expression{read}})}, `{"flag":false}`},
		{"if-type", Expression{Op: "if", When: &read, Then: expressionPointer(Expression{Op: "array"})}, `0`},
		{"choose", Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/type")), Cases: map[string]Expression{"text": {Op: "object", Fields: map[string]Expression{"content": readExpression("input", "/text")}}}}, `{"type":"text","text":"hello"}`},
		{"choose-missing", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": {Op: "object"}}}, `"unknown"`},
		{"choose-type", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": {Op: "object"}}}, `true`},
		{"required-order", objectExpression(map[string]Expression{"z": {Op: "read", Path: "/z"}, "a": {Op: "read", Path: "/a", Required: true}}), `{}`},
		{"merge-last", Expression{Op: "merge", Policy: "last", Items: []Expression{read, objectExpression(map[string]Expression{"a": literalExpression("<&"), "c": {Op: "array", Items: []Expression{read}}})}}, `{"a":1,"b": [ 1e+3 ],"a":2}`},
		{"merge-first", Expression{Op: "merge", Policy: "first", Items: []Expression{read, objectExpression(map[string]Expression{"a": literalExpression("new")})}}, `{"a":1}`},
		{"merge-collision", Expression{Op: "merge", Policy: "reject", Items: []Expression{read, read}}, `{"z":1,"a":2}`},
		{"merge-type", Expression{Op: "merge", Policy: "last", Items: []Expression{read}}, `null`},
		{"merge-priority", Expression{Op: "merge", Policy: "reject", Items: []Expression{read, {Op: "read", Path: "/missing", Required: true}}}, `{"a":1}`},
		{"keys-raw", Expression{Op: "keys", Source: &read}, `{"z":1,"a":null,"\u0061":false,"<":{},"\u2028":[]}`},
		{"keys-constructed", Expression{Op: "keys", Source: expressionPointer(objectExpression(map[string]Expression{"bad\xff": read, "bad\ufffd": literalExpression(1), "<": read}))}, `"body"`},
		{"keys-null", Expression{Op: "keys", Source: &read}, `null`},
		{"keys-missing", Expression{Op: "keys", Source: expressionPointer(readExpression("input", "/missing"))}, `{}`},
		{"concat", Expression{Op: "concat", Items: []Expression{read, {Op: "array", Items: []Expression{read}}, read}}, `[ {"k":"<"}, [ 1, 2 ], null ]`},
		{"concat-null", Expression{Op: "concat", Items: []Expression{read}}, `null`},
		{"filter", Expression{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))}, `[{"keep":false},{"keep":true,"value":"<"}]`},
		{"filter-empty", Expression{Op: "filter", Source: &read, When: expressionPointer(literalExpression(false))}, `[null,1]`},
		{"filter-type", Expression{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))}, `[{"keep":null}]`},
		{"sort-stable", Expression{Op: "sort", Source: &read, Key: "/id"}, `[{"id":2,"value":"first"},{"id":1},{"id":2,"value":"last"}]`},
		{"sort-exact", Expression{Op: "sort", Source: &read, Key: "/id"}, `[{"id":9007199254740993},{"id":9007199254740992},{"id":1e+3}]`},
		{"sort-constructed", Expression{Op: "sort", Source: expressionPointer(Expression{Op: "array", Items: []Expression{read, read}}), Key: "/id"}, `{"id":"a","body": [ 1e+3 ]}`},
		{"sort-missing-priority", Expression{Op: "sort", Source: &read, Key: "/id"}, `[{"id":{}},{"id":1},{}]`},
		{"sort-single-container", Expression{Op: "sort", Source: &read, Key: "/id"}, `[{"id":{}}]`},
		{"sort-type", Expression{Op: "sort", Source: &read, Key: "/id"}, `[{"id":{}},{"id":1}]`},
		{"associate", Expression{Op: "associate", Source: &read, Key: "/id"}, `[ {"id":"b","value": [ 1e+3 ]}, {"id":"a","value":"<"} ]`},
		{"associate-duplicate", Expression{Op: "associate", Source: &read, Key: "/id"}, `[{"id":"a"},{"id":"a"}]`},
		{"associate-empty", Expression{Op: "associate", Source: &read, Key: "/id"}, `[{"id":""}]`},
		{"associate-missing-priority", Expression{Op: "associate", Source: &read, Key: "/id"}, `[{"id":1},{}]`},
		{"join-items", Expression{Op: "join", Items: []Expression{literalExpression("<"), read, literalExpression("尾")}}, `" & "`},
		{"join-source", Expression{Op: "join", Source: expressionPointer(Expression{Op: "map", Source: &read, Body: &text})}, `[{"text":"a"},{"text":"<"}]`},
		{"join-source-type", Expression{Op: "join", Source: &read}, `["a",null]`},
		{"join-evaluate-first", Expression{Op: "join", Items: []Expression{readExpression("input", "/bad"), {Op: "read", Path: "/missing", Required: true}}}, `{"bad":1}`},
		{"parse-json", Expression{Op: "parse_json", Source: &read}, `" {\"a\":9007199254740993,\"a\":1e+3} "`},
		{"parse-invalid", Expression{Op: "parse_json", Source: &read}, `"{"`},
		{"parse-type", Expression{Op: "parse_json", Source: &read}, `{}`},
		{"stringify-raw", Expression{Op: "stringify_json", Source: &read}, ` { "a":1e+3,"a":"<" } `},
		{"stringify-constructed", Expression{Op: "stringify_json", Source: expressionPointer(objectExpression(map[string]Expression{"array": {Op: "array", Items: []Expression{read}}, "missing": {Op: "omit"}}))}, `{"a":"<"}`},
		{"stringify-absent", Expression{Op: "stringify_json", Source: expressionPointer(Expression{Op: "omit"})}, `null`},
		{"exists-constructed", Expression{Op: "exists", Source: expressionPointer(Expression{Op: "array", Items: []Expression{read}})}, `null`},
		{"present-null", Expression{Op: "present", Source: expressionPointer(Expression{Op: "parse_json", Source: &read})}, `" null "`},
		{"present-constructed", Expression{Op: "present", Source: expressionPointer(Expression{Op: "object"})}, `null`},
		{"not-constructed", Expression{Op: "not", Source: expressionPointer(Expression{Op: "equal", Items: []Expression{Expression{Op: "array", Items: []Expression{read}}, literalExpression([]int{1})}})}, `1`},
		{"enum-constructed", Expression{Op: "enum", Source: expressionPointer(Expression{Op: "join", Items: []Expression{literalExpression("pre-"), read}}), Values: map[string]Value{"pre-value": StringValue("mapped")}}, `"value"`},
		{"enum-unmapped", Expression{Op: "enum", Source: &read, Values: map[string]Value{"known": StringValue("mapped")}}, `"unknown"`},
		{"enum-default", Expression{Op: "enum", Source: &read, Values: map[string]Value{"known": StringValue("mapped")}, Otherwise: expressionPointer(literalExpression(false))}, `"unknown"`},
		{"enum-lazy-default", Expression{Op: "enum", Source: &read, Values: map[string]Value{"known": StringValue("mapped")}, Otherwise: expressionPointer(Expression{Op: "read", Path: "/absent", Required: true})}, `"known"`},
		{"cast-constructed", Expression{Op: "cast", Source: expressionPointer(Expression{Op: "stringify_json", Source: &read}), To: NumberType}, `9007199254740993`},
		{"cast-container", Expression{Op: "cast", Source: expressionPointer(Expression{Op: "array", Items: []Expression{read}}), To: StringType}, `1`},
		{"strip-constructed", Expression{Op: "strip_prefix", Source: expressionPointer(Expression{Op: "join", Items: []Expression{literalExpression("prefix:"), read}}), Value: StringValue("prefix:")}, `"text"`},
		{"strip-missing", Expression{Op: "strip_prefix", Source: &read, Value: StringValue("prefix:")}, `"text"`},
		{"trim-missing", Expression{Op: "trim_prefix", Source: &read, Value: StringValue("prefix:")}, `"text"`},
		{"equal-constructed-exact", Expression{Op: "equal", Items: []Expression{objectExpression(map[string]Expression{"n": literalExpression(1)}), read}}, `{"n":1.0,"n":1e0}`},
		{"equal-normalized-keys", Expression{Op: "equal", Items: []Expression{objectExpression(map[string]Expression{"bad\xff": literalExpression(1), "bad�": literalExpression(2)}), read}}, `{"bad�":1}`},
		{"equal-absent", Expression{Op: "equal", Items: []Expression{{Op: "omit"}, readExpression("input", "/missing")}}, `{}`},
		{"equal-null", Expression{Op: "equal", Items: []Expression{{Op: "array"}, read}}, `null`},
		{"all-constructed", Expression{Op: "all", Items: []Expression{{Op: "exists", Source: expressionPointer(Expression{Op: "object"})}, {Op: "not", Source: &read}}}, `false`},
		{"all-single", Expression{Op: "all", Items: []Expression{read}}, `true`},
		{"any-single", Expression{Op: "any", Items: []Expression{read}}, `false`},
		{"all-three", Expression{Op: "all", Items: []Expression{literalExpression(true), read, literalExpression(false)}}, `true`},
		{"all-omitted-operand", Expression{Op: "all", Items: []Expression{{Op: "omit"}, read}}, `true`},
		{"all-evaluate-first", Expression{Op: "all", Items: []Expression{readExpression("input", "/bad"), {Op: "read", Path: "/missing", Required: true}}}, `{"bad":1}`},
		{"any-no-short-circuit", Expression{Op: "any", Items: []Expression{literalExpression(true), read}}, `1`},
		{"any-three-evaluate-first", Expression{Op: "any", Items: []Expression{literalExpression(true), readExpression("input", "/bad"), {Op: "read", Path: "/missing", Required: true}}}, `{"bad":1}`},
		{"if-constructed-condition", Expression{Op: "if", When: expressionPointer(Expression{Op: "equal", Items: []Expression{Expression{Op: "array", Items: []Expression{read}}, literalExpression([]int{1})}}), Then: expressionPointer(literalExpression("matched"))}, `1`},
		{"choose-constructed", Expression{Op: "choose", Source: expressionPointer(Expression{Op: "stringify_json", Source: &read}), Cases: map[string]Expression{"null": literalExpression("matched")}}, `null`},
		{"filter-constructed-condition", Expression{Op: "filter", Source: &read, When: expressionPointer(Expression{Op: "equal", Items: []Expression{objectExpression(map[string]Expression{"id": readExpression("item", "/id")}), literalExpression(map[string]int{"id": 1})}})}, `[{"id":1},{"id":2}]`},
		{"scope-constructed", Expression{Op: "map", Source: &read, Body: expressionPointer(Expression{Op: "join", Items: []Expression{text, readExpression("root", "/0/text"), readExpression("context", "")}})}, `[{"text":"first"},{"text":"second"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled := compileTestOutputExpression(t, tc.expression)
			plan, ok := compileOutputPlan(compiled)
			if !ok {
				t.Fatal("eligible expression rejected")
			}
			input, err := ParseValue([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			compareOutputPlan(t, compiled, plan, input, DefaultLimits(), 0, 0)
			for limit := 1; limit <= 96; limit++ {
				limits := DefaultLimits()
				limits.BufferBytes = limit
				compareOutputPlan(t, compiled, plan, input, limits, 0, 0)
			}
			for limit := 1; limit <= 12; limit++ {
				compareOutputPlan(t, compiled, plan, input, DefaultLimits(), limit, 0)
				compareOutputPlan(t, compiled, plan, input, DefaultLimits(), 0, limit)
			}
		})
	}
}

func compareOutputPlan(t *testing.T, expression *compiledExpression, plan *compiledOutputPlan, input Value, limits Limits, stepLimit, cancelAt int) {
	t.Helper()
	state := func() evaluation {
		var ctx context.Context = t.Context()
		if cancelAt != 0 {
			ctx = &outputPlanCancelContext{Context: ctx, remaining: cancelAt}
		}
		return evaluation{ctx: ctx, input: input, root: input, context: StringValue("context"), budget: &evaluationBudget{limits: limits, stepLimit: stepLimit}}
	}
	originalState, plannedState := state(), state()
	plannedState.objectReader = plan.newRootObjectReader(input)
	want, wantErr := expression.evaluate(originalState)
	got, gotErr := plan.evaluate(plannedState)
	if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || mappingErrorPath("", gotErr) != mappingErrorPath("", wantErr) || originalState.budget.steps != plannedState.budget.steps {
		t.Fatalf("buffer=%d steps=%d cancel=%d: got %s / %v (%s, %d steps), want %s / %v (%s, %d steps)", limits.BufferBytes, stepLimit, cancelAt, got.raw, gotErr, mappingErrorPath("", gotErr), plannedState.budget.steps, want.raw, wantErr, mappingErrorPath("", wantErr), originalState.budget.steps)
	}
	var wantLimit, gotLimit *ResourceLimitError
	if errors.As(wantErr, &wantLimit) != errors.As(gotErr, &gotLimit) || !reflect.DeepEqual(wantLimit, gotLimit) || errors.Is(wantErr, context.Canceled) != errors.Is(gotErr, context.Canceled) {
		t.Fatal("error chain changed", gotErr, wantErr)
	}
}

type outputPlanCancelContext struct {
	context.Context
	remaining int
}

func (ctx *outputPlanCancelContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestOutputPlanSelectsBeforeExecution(t *testing.T) {
	unknown := &compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: "future_operation"}
	for _, expression := range []*compiledExpression{
		unknown,
		{op: "map", expressionCompileFacts: &expressionCompileFacts{source: unknown, body: &compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: "literal"}}},
		{op: "if", expressionCompileFacts: &expressionCompileFacts{when: &compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: "literal"}, then: unknown, otherwise: &compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: "object"}}},
	} {
		if _, ok := compileOutputPlan(expression); ok {
			t.Fatal("unsupported construction selected", expression.op)
		}
	}
}

func TestOutputPlanCoversCatalog(t *testing.T) {
	for _, operation := range expressionOperations {
		if operation.Name == "ref" {
			continue // References are expanded before plan compilation.
		}
		group := "historical"
		switch operation.Name {
		case "type", "is_integer", "integer", "let", "index", "walk", "recur", "capture_native", "replay_native", "native_matches", "split_data_uri", "with_index", "remember", "recall", "keys", "merge_deep", "entries", "from_entries", "group_runs", "equal_text":
			group = "plan-only"
		}
		t.Run(group+"/"+operation.Name, func(t *testing.T) {
			if _, ok := compileOutputPlan(&compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: operation.Name}); !ok {
				t.Fatal("catalog operation has no compiled plan", operation.Name)
			}
		})
	}
}

func TestOutputPlanLogicalNodeLimit(t *testing.T) {
	read := readExpression("input", "")
	expression := compileTestOutputExpression(t, Expression{Op: "flatmap", Source: &read, Body: expressionPointer(readExpression("item", ""))})
	plan, _ := compileOutputPlan(expression)
	input, _ := ParseValue([]byte(`[[1,2],[3,4]]`))
	limits := DefaultLimits()
	limits.Nodes = 3
	compareOutputPlan(t, expression, plan, input, limits, 100, 0)
}

func BenchmarkOutputPlanNestedPayload(b *testing.B) {
	expression := readExpression("item", "/text")
	for range 4 {
		expression = objectExpression(map[string]Expression{"nested": expression})
	}
	body := expression
	expression = Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: &body}
	compiled := compileTestOutputExpression(b, expression)
	plan, ok := compileOutputPlan(compiled)
	if !ok {
		b.Fatal("output plan not selected")
	}
	input, err := ParseValue([]byte(`[{"text":"` + strings.Repeat("p", 1<<20) + `"}]`))
	if err != nil {
		b.Fatal(err)
	}
	for _, isPlanned := range []bool{false, true} {
		name := "expression"
		if isPlanned {
			name = "output-plan"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				state := evaluation{ctx: b.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits()}}
				var value Value
				var err error
				if isPlanned {
					value, err = plan.evaluate(state)
				} else {
					value, err = compiled.evaluate(state)
				}
				if err != nil || value.Len() < 1<<20 {
					b.Fatal("incomplete nested output", err)
				}
			}
		})
	}
}
