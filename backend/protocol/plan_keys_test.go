package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestKeysTypeAndNormalization(t *testing.T) {
	read := readExpression("input", "")
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: &read})
	plan, _ := compileOutputPlan(expression)
	if expression.coreType != nil {
		t.Fatal("object keys acquired core provenance")
	}
	for _, raw := range []string{"null", "[]", "true", "0", `"object"`, ""} {
		state := evaluation{ctx: t.Context(), input: Value{raw: raw}, budget: &evaluationBudget{limits: DefaultLimits()}}
		if _, err := plan.evaluate(state); err == nil || !strings.Contains(err.Error(), "expected JSON object") {
			t.Fatal("non-object keys source was accepted", raw, err)
		}
	}
	for _, source := range []*Expression{nil, expressionPointer(literalExpression(nil)), expressionPointer(literalExpression([]int{})), expressionPointer(literalExpression("object"))} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compile(Expression{Op: "keys", Source: source}, "/keys", expressionScope{}, 1); err == nil {
			t.Fatal("missing or statically non-object source compiled")
		}
	}
	for _, kind := range []JSONType{StringType, IntegerType} {
		err := checkExpressionOutput(expression, &ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: kind}}, DefaultLimits())
		if (err == nil) != (kind == StringType) {
			t.Fatal("keys output type was not checked", kind, err)
		}
	}
	for _, raw := range []string{`{"bad\ufffd":0,"bad\ud800":1,"z":2}`, "{\"bad\xff\":0,\"bad\ufffd\":1,\"z\":2}"} {
		input := parseTestValue(t, raw)
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}}
		output, err := plan.evaluate(state)
		if err != nil || output != fixtureValue(t, []string{"bad\ufffd", "z"}) {
			t.Fatal("decoded key collisions were not normalized and folded", output, err)
		}
	}
}

func TestKeysOwnsNamesWithoutRetainingPayloads(t *testing.T) {
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	input := Value{raw: `{"small":"` + strings.Repeat("<", 1<<20) + `"}`}
	reads := 0
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func([]string) (JSONOutput, error) {
		reads++
		return JSONLeaf(input), nil
	}}
	output, err := plan.build(state)
	if err != nil || reads != 1 || state.budget.steps != 2 {
		t.Fatal("keys repeated its source or changed expression steps", reads, err)
	}
	if output.output.node.origin != 0 || len(output.output.node.items) != 1 {
		t.Fatal("keys retained the source's shape or provenance")
	}
	name := output.output.node.items[0].value
	if !name.IsString("small") || sharesValueStorage(input, name) {
		t.Fatal("a retained key pinned the source payload")
	}
}

func TestKeysMeasuresEscapingBeforeOutput(t *testing.T) {
	key := strings.Repeat("<", 128)
	input := Value{raw: `{"` + key + `":0}`}
	expected := fixtureValue(t, []string{key})
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	for _, limit := range []int{input.Len(), expected.Len() - 1, expected.Len(), expected.Len() + 1} {
		limits := DefaultLimits()
		limits.BufferBytes = limit
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}}
		output, err := plan.evaluate(state)
		if limit >= expected.Len() {
			if err != nil || output != expected {
				t.Fatal("exact key output budget rejected", limit, output, err)
			}
			continue
		}
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Domain != "evaluation" || failure.Metric != "bytes" || failure.Observed != expected.Len() || !output.IsZero() {
			t.Fatal("escaped key output bypassed its budget or changed the failure", limit, output, err)
		}
	}
}

func TestKeysTypedRequestObjectParity(t *testing.T) {
	checkTypedRequestObjectQuery(t, "keys")
}

func TestTypeTypedRequestObjectParity(t *testing.T) {
	checkTypedRequestObjectQuery(t, "type")
}

func checkTypedRequestObjectQuery(t *testing.T, operation string) {
	t.Helper()
	parameters := Object{"bad\xff": StringValue("first"), "bad\ufffd": StringValue("last"), "<>&\u2028": Value{raw: "null"}, "null": Value{raw: "null"}, "number": Value{raw: "9007199254740993"}, "payload": Value{raw: ` { "body":"` + strings.Repeat("<", 32<<10) + `" } `}}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Parameters: parameters,
		Content: []Node{{Attributes: parameters, Payload: Value{raw: `{"nested":{"own":true}}`}, Children: []Node{{Attributes: parameters}}}},
		Tools:   []Tool{{Options: parameters}},
		Native:  &Native{Value: Value{raw: "null"}, RequestContext: &NativeRequestContext{Values: parameters}},
	}
	for _, tc := range []struct {
		name, from, path string
		request          *Request
		isRequired       bool
		stepLimit        int
		cancelAt         int
	}{
		{"parameters", "input", "/parameters", request, false, 0, 0},
		{"root", "root", "/parameters", request, false, 0, 0},
		{"tool-options", "input", "/tools/0/options", request, false, 0, 0},
		{"nested-node", "input", "/content/0/children/0/attributes", request, false, 0, 0},
		{"native-context", "input", "/native/requestContext/values", request, false, 0, 0},
		{"missing", "input", "/parameters", &Request{}, false, 0, 0},
		{"empty-required", "input", "/parameters", &Request{Parameters: Object{}}, true, 0, 0},
		{"null-slice", "input", "/content/0/attributes", &Request{}, false, 0, 0},
		{"empty-slice", "input", "/content/0/attributes", &Request{Content: []Node{}}, false, 0, 0},
		{"invalid-index", "input", "/content/00/attributes", request, false, 0, 0},
		{"opaque-value", "input", "/content/0/payload/nested", request, false, 0, 0},
		{"opaque-null", "input", "/parameters/null", request, false, 0, 0},
		{"read-step-limit", "input", "/parameters", request, false, 1, 0},
		{"exact-steps", "input", "/parameters", request, false, 2, 0},
		{"query-cancellation", "input", "/parameters", request, false, 0, 1},
		{"read-cancellation", "input", "/parameters", request, false, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := measureSemanticValue(tc.request, DefaultLimits()); err != nil {
				t.Fatal("typed fixture must pass its input budget", err)
			}
			read := readExpression(tc.from, tc.path)
			read.Required = tc.isRequired
			plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: operation, Source: &read}))
			run := func(hasRequest bool) (Value, int, int, error) {
				ctx := t.Context()
				if tc.cancelAt != 0 {
					ctx = &outputPlanCancelContext{Context: ctx, remaining: tc.cancelAt}
				}
				reads := 0
				reader := func(pointer []string) (JSONOutput, error) {
					reads++
					return readCoreRequestOutput(tc.request, pointer)
				}
				state := evaluation{ctx: ctx, inputReader: reader, rootReader: reader, budget: &evaluationBudget{limits: DefaultLimits(), stepLimit: tc.stepLimit}}
				if hasRequest {
					state.coreRequest = tc.request
				}
				output, err := plan.evaluate(state)
				return output, state.budget.steps, reads, err
			}
			want, wantSteps, _, wantErr := run(false)
			got, gotSteps, reads, gotErr := run(true)
			if got != want || gotSteps != wantSteps || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatal("typed query changed output, error chain or steps", got, gotErr, gotSteps, want, wantErr, wantSteps)
			}
			if plan.hasRequestObjectSource && reads != 0 {
				t.Fatal("typed Object query still constructed member values")
			}
			if plan.expression.coreType != nil {
				t.Fatal("shape query acquired core provenance")
			}
		})
	}
}

func TestKeysTypedRequestInputAndOutputBudgets(t *testing.T) {
	checkTypedRequestQueryBudgets(t, "keys")
}

func TestTypeTypedRequestInputAndOutputBudgets(t *testing.T) {
	checkTypedRequestQueryBudgets(t, "type")
}

func checkTypedRequestQueryBudgets(t *testing.T, operation string) {
	t.Helper()
	document := textProtocolPackage(t)
	document.Spec.WireContracts[EncodeRequest].Schema.AllowUnknown = true
	document.Mapping.Directions[EncodeRequest].Program.Fields["query"] = Expression{Op: operation, Source: expressionPointer(readExpression("input", "/parameters"))}
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequestValue(t.Context(), Value{raw: `{"model":"m","prompt":"hello"}`}, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	var queryPlan *compiledOutputPlan
	for _, field := range compiled.mappings[EncodeRequest].outputPlan.fields {
		if field.key == "query" {
			queryPlan = field.expression
		}
	}
	if queryPlan == nil || !queryPlan.hasRequestObjectSource {
		t.Fatal("request Object selector was not compiled")
	}
	for _, tc := range []struct {
		name                     string
		parameters               Object
		limit                    int64
		wantError, wantTypeError bool
	}{
		{"absent-value", Object{"invalid": {}}, 1024, true, true},
		{"normalized-key-collision", Object{"bad\xff": StringValue("first"), "bad\ufffd": Value{raw: "null"}}, 1024, false, false},
		{"whole-input-limit", Object{strings.Repeat("<", 20): StringValue("value")}, 16, true, true},
		{"escaped-output-limit", Object{strings.Repeat("<", 20): StringValue("value")}, 64, true, false},
		{"escaped-output-success", Object{strings.Repeat("<", 20): StringValue("value")}, 256, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request.Parameters = tc.parameters
			ctx, err := WithRequestBodyLimit(t.Context(), tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			queryPlan.hasRequestObjectSource = false
			want, wantErr := compiled.EncodeRequestValue(ctx, request, EvaluationContext{})
			queryPlan.hasRequestObjectSource = true
			got, gotErr := compiled.EncodeRequestValue(ctx, request, EvaluationContext{})
			shouldFail := tc.wantError
			if operation == "type" {
				shouldFail = tc.wantTypeError
			}
			if (gotErr != nil) != shouldFail || got != want || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatal("shape query bypassed input validation or changed a resource boundary", gotErr, wantErr)
			}
			if tc.name == "absent-value" && !strings.Contains(gotErr.Error(), "absent JSON value requires omission") {
				t.Fatal("invalid Value member was not rejected before the shape query", gotErr)
			}
			if gotErr == nil {
				raw, rawErr := compiled.executeMapping(context.Background(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
				if rawErr != nil || raw != got {
					t.Fatal("raw execution diverged from the typed query", rawErr, raw, got)
				}
			}
		})
	}
}

func TestKeysTypedRequestKeepsIterationAndBindingScopes(t *testing.T) {
	keys := func(from, path string) Expression {
		return Expression{Op: "keys", Source: expressionPointer(readExpression(from, path))}
	}
	child := readExpression("item", "/children")
	program := objectExpression(map[string]Expression{
		"types": objectExpression(map[string]Expression{
			"binding": {Op: "let", Name: "local", Source: expressionPointer(literalExpression(map[string]any{"parameters": nil})), Body: expressionPointer(Expression{Op: "type", Source: expressionPointer(bindingTestRead("local", "/parameters"))})},
			"item":    {Op: "map", Source: expressionPointer(literalExpression([]any{map[string]any{"parameters": []any{}}})), Body: expressionPointer(Expression{Op: "type", Source: expressionPointer(readExpression("item", "/parameters"))})},
			"root":    {Op: "type", Source: expressionPointer(readExpression("root", "/parameters"))},
		}),
		"binding": {Op: "let", Name: "local", Source: expressionPointer(literalExpression(map[string]any{"parameters": map[string]any{"bound": true}})), Body: expressionPointer(Expression{Op: "keys", Source: expressionPointer(bindingTestRead("local", "/parameters"))})},
		"items": {Op: "map", Source: expressionPointer(literalExpression([]any{map[string]any{"parameters": map[string]any{"item": true}}})), Body: expressionPointer(objectExpression(map[string]Expression{
			"item": keys("item", "/parameters"), "root": keys("root", "/parameters"),
		}))},
		"walk": {Op: "walk", Source: expressionPointer(literalExpression([]any{map[string]any{"children": []any{map[string]any{}}}})), Body: expressionPointer(objectExpression(map[string]Expression{
			"root":     keys("input", "/parameters"),
			"children": {Op: "if", When: &Expression{Op: "exists", Source: &child}, Then: &Expression{Op: "recur", Source: &child}},
		}))},
	})
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, program))
	request := &Request{Parameters: Object{"root": Value{raw: "null"}}}
	reader := func(pointer []string) (JSONOutput, error) { return readCoreRequestOutput(request, pointer) }
	state := evaluation{ctx: t.Context(), coreRequest: request, inputReader: reader, rootReader: reader, budget: &evaluationBudget{limits: DefaultLimits()}}
	got, err := plan.evaluate(state)
	want := parseTestValue(t, `{"binding":["bound"],"items":[{"item":["item"],"root":["root"]}],"types":{"binding":"null","item":["array"],"root":"object"},"walk":[{"children":[{"root":["root"]}],"root":["root"]}]}`)
	if err != nil || got != want {
		t.Fatal("typed keys read the request in place of a local scope or lost its root", got, err)
	}
}
