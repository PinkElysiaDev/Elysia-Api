package protocol

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func charsetExpression(source Expression, alphabet string) Expression {
	return Expression{Op: "matches_charset", Source: &source, Value: StringValue(alphabet)}
}

func TestCharacterSetRawAndPlannedStrings(t *testing.T) {
	for _, fixture := range []struct {
		name, raw, alphabet string
		isMatch             bool
	}{
		{"ascii", `"aA0_-"`, "Aa012_-", true},
		{"case", `"A"`, "a", false},
		{"unicode", `"猫😀é"`, "é猫😀猫", true},
		{"escaped", `"\u732b\ud83d\ude00"`, "猫😀", true},
		{"normalization", `"e\u0301"`, "é", false},
		{"combining", `"e\u0301"`, "e\u0301", true},
		{"nul", `"\u0000"`, "\x00", true},
		{"empty-input", `""`, "abc", true},
		{"empty-both", `""`, "", true},
		{"empty-alphabet", `"a"`, "", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			input := numericTestInput(t, fixture.raw)
			raw := charsetExpression(readExpression("input", ""), fixture.alphabet)
			constructed := Expression{Op: "with_input", Source: expressionPointer(objectExpression(map[string]Expression{
				"text": readExpression("input", ""),
			})), Body: expressionPointer(charsetExpression(readExpression("input", "/text"), fixture.alphabet))}
			literal := charsetExpression(Expression{Op: "literal", Value: input}, fixture.alphabet)
			for _, expression := range []Expression{raw, constructed, literal} {
				compiled := compileTestOutputExpression(t, expression)
				plan, hasPlan := compileOutputPlan(compiled)
				if !hasPlan {
					t.Fatal("character-set predicate has no output plan")
				}
				value, err := plan.evaluate(numericTestState(t, input))
				if err != nil || value != coreBoolean(fixture.isMatch) {
					t.Fatal("character-set membership changed", value, err)
				}
			}
		})
	}
}

func TestCharacterSetRejectsInvalidTypesAndFields(t *testing.T) {
	read := readExpression("input", "")
	plan := numericTestPlan(t, charsetExpression(read, "abc"))
	constructed := numericTestPlan(t, Expression{Op: "with_input", Source: expressionPointer(objectExpression(map[string]Expression{
		"text": read,
	})), Body: expressionPointer(charsetExpression(readExpression("input", "/text"), "abc"))})
	for _, raw := range []string{"", "null", "1", "false", "[]", "{}"} {
		input := numericTestInput(t, raw)
		for _, current := range []*compiledOutputPlan{plan, constructed} {
			if _, err := current.evaluate(numericTestState(t, input)); err == nil {
				t.Fatal("non-string source was coerced", raw)
			}
		}
		compiler := newExpressionCompiler(DefaultLimits(), nil)
		if _, err := compiler.compilePlan(Expression{Op: "matches_charset", Source: &read, Value: input}, "/charset", expressionScope{}, 1); err == nil {
			t.Fatal("non-string alphabet compiled", raw)
		}
	}
	for _, raw := range []string{
		`{"op":"matches_charset","value":"a"}`,
		`{"op":"matches_charset","source":{"op":"literal","value":false},"value":"a"}`,
		`{"op":"matches_charset","source":{"op":"read"},"value":"a","otherwise":null}`,
	} {
		var expression Expression
		if err := decodeContract([]byte(raw), &expression); err != nil {
			t.Fatal(err)
		}
		compiler := newExpressionCompiler(DefaultLimits(), nil)
		if _, err := compiler.compilePlan(expression, "/charset", expressionScope{}, 1); err == nil {
			t.Fatal("invalid character-set declaration compiled", raw)
		}
	}
	var expression Expression
	if err := decodeContract([]byte(`{"op":"matches_charset","source":{"op":"read"},"value":"a","pattern":"a"}`), &expression); err == nil {
		t.Fatal("unknown character-set field passed contract decoding")
	}
}

func TestCharacterSetUsesSharedBudgets(t *testing.T) {
	plan := numericTestPlan(t, charsetExpression(readExpression("input", ""), "abc"))
	state := numericTestState(t, StringValue("abc"))
	state.budget.stepLimit = 2
	if value, err := plan.evaluate(state); err != nil || value != coreBoolean(true) || state.budget.steps != 2 {
		t.Fatal("exact expression budget changed", value, state.budget.steps, err)
	}
	state = numericTestState(t, StringValue("abc"))
	state.budget.stepLimit = 1
	_, err := plan.evaluate(state)
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "steps" || failure.Observed != 2 {
		t.Fatal("predicate bypassed the step boundary", err)
	}
	state = numericTestState(t, StringValue("abc"))
	state.budget.limits.BufferBytes = 2
	if _, err := plan.evaluate(state); !errors.As(err, &failure) || failure.Metric != "bytes" {
		t.Fatal("predicate bypassed its source byte budget", err)
	}
	state = numericTestState(t, StringValue("abc"))
	state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 2}
	if _, err := plan.evaluate(state); !errors.Is(err, context.Canceled) || state.budget.steps != 2 {
		t.Fatal("predicate bypassed cancellation before reading its source", state.budget.steps, err)
	}
}

func TestCharacterSetReferencesReleaseGraphs(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"alphabet": charsetExpression(readExpression("input", ""), "ba猫a"),
	})
	call := Expression{Op: "ref", Ref: "alphabet"}
	first, err := compiler.compilePlan(call, "/first", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiler.compilePlan(call, "/second", expressionScope{bindingDepth: 3}, 1)
	if err != nil || first != second || !slices.Equal(first.expression.keys, []string{"a", "b", "猫"}) {
		t.Fatal("pure reference did not share its immutable finite alphabet", err)
	}
	compiler.references["alphabet"] = charsetExpression(readExpression("input", ""), "z")
	expressions := compiler.compiledExpressions
	compiler.releaseGraph()
	for _, expression := range expressions {
		if expression.expressionCompileFacts != nil {
			t.Fatal("character-set instruction retained its compilation graph")
		}
	}
	for _, fixture := range []struct {
		input   string
		isMatch bool
	}{{"猫ab", true}, {"z", false}, {"", true}} {
		for _, plan := range []*compiledOutputPlan{first, second} {
			state := numericTestState(t, StringValue(fixture.input))
			value, err := plan.evaluate(state)
			if err != nil || value != coreBoolean(fixture.isMatch) || state.budget.steps != 2 {
				t.Fatal("shared reference retained another input or lost its alphabet", value, state.budget.steps, err)
			}
		}
	}
}

func TestCharacterSetCatalogAndSourceIdentity(t *testing.T) {
	operations := DescribeSchema().Operations
	index := slices.IndexFunc(operations, func(operation OperationInfo) bool { return operation.Name == "matches_charset" })
	if index < 0 || operations[index].Result != BooleanType || !slices.Equal(operations[index].Fields, []string{"source", "value"}) || !slices.Equal(operations[index].Required, []string{"source", "value"}) {
		t.Fatal("schema/editor catalog lost the predicate contract")
	}
	document := textProtocolPackage(t)
	document.Spec.Expressions = map[string]Expression{"model-alphabet": charsetExpression(readExpression("input", ""), "m")}
	request := document.Spec.Documents["request"]
	request.Assertions = []WireAssertion{{Path: "/model", Check: &Expression{Op: "ref", Ref: "model-alphabet"}, Reason: "model must use the declared alphabet"}}
	document.Spec.Documents["request"] = request
	first := compilePackageForTest(t, document)
	document.Spec.Expressions["model-alphabet"] = charsetExpression(readExpression("input", ""), "mx")
	second := compilePackageForTest(t, document)
	if first.Hash() == second.Hash() || first.mappings[DecodeRequest].definitionHash == second.mappings[DecodeRequest].definitionHash {
		t.Fatal("changed alphabet reused a source or direction identity")
	}
	body := []byte(`{"model":"x","prompt":"hi"}`)
	if _, err := first.DecodeRequest(t.Context(), body, EvaluationContext{}); err == nil {
		t.Fatal("earlier compiled alphabet was changed by a source edit")
	}
	if _, err := second.DecodeRequest(t.Context(), body, EvaluationContext{}); err != nil {
		t.Fatal("edited alphabet was not applied", err)
	}
}

func TestCharacterSetScopeKeepsReadPermissions(t *testing.T) {
	for _, path := range []string{"/role", "/attributes/wire:private"} {
		compiled := compileTestOutputExpression(t, charsetExpression(readExpression("input", path), "user"))
		err := checkScopeExpression(compiled, make(map[*compiledExpression]bool))
		if (err == nil) != (path == "/role") {
			t.Fatal("character-set predicate changed scope read permissions", path, err)
		}
	}
}
