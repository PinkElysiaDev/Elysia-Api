package protocol

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
)

func TestIntegralNumberForms(t *testing.T) {
	read := readExpression("input", "")
	plan := numericTestPlan(t, Expression{Op: "is_integral", Source: &read})
	for _, fixture := range []struct {
		raw        string
		isIntegral bool
	}{
		{"", false}, {"null", false}, {`"1"`, false}, {"true", false}, {"[]", false}, {"{}", false},
		{"0", true}, {"-0.000", true}, {" 42.00 ", true}, {"1.5", false},
		{"1.0", true}, {"1e0", true}, {"1E+2", true}, {"10e-1", true}, {"10e-2", false},
		{"1.20e1", true}, {"1.20e0", false}, {"0.00100e3", true}, {"0.00100e2", false},
		{"1000.00e-3", true}, {"1000.00e-4", false}, {"-12.50e+1", true},
		{"9007199254740993", true}, {"9223372036854775808.0", true}, {"-9223372036854775809", true},
		{"1e" + strings.Repeat("9", 2048), true}, {"1e-" + strings.Repeat("9", 2048), false},
		{"0e-" + strings.Repeat("9", 2048), true}, {"1e+" + strings.Repeat("0", 2048), true},
		{"1" + strings.Repeat("0", 8192) + "e-8192", true},
		{"1" + strings.Repeat("0", 8192) + "e-8193", false},
	} {
		input := numericTestInput(t, fixture.raw)
		state := numericTestState(t, input)
		value, err := plan.evaluate(state)
		if err != nil || value != coreBoolean(fixture.isIntegral) || state.budget.steps != 2 {
			t.Fatalf("integrality changed for %.80q: %s / %v, steps %d", fixture.raw, value.raw, err, state.budget.steps)
		}
	}
	for _, operation := range []string{"object", "array"} {
		source := Expression{Op: operation}
		plan := numericTestPlan(t, Expression{Op: "is_integral", Source: &source})
		value, err := plan.evaluate(numericTestState(t, Value{}))
		if err != nil || value != coreBoolean(false) {
			t.Fatal("constructed container was treated as numeric", operation, value, err)
		}
	}
}

func TestIntegralNumberMatchesRationalArithmetic(t *testing.T) {
	for coefficient := -41; coefficient <= 41; coefficient++ {
		for fractionDigits := 0; fractionDigits <= 4; fractionDigits++ {
			for exponent := -6; exponent <= 6; exponent++ {
				mantissa := fmt.Sprintf("%d", coefficient)
				if fractionDigits != 0 {
					mantissa += "." + strings.Repeat("0", fractionDigits-1) + "5"
				}
				raw := fmt.Sprintf("%se%d", mantissa, exponent)
				number, ok := new(big.Rat).SetString(raw)
				if !ok {
					t.Fatal("invalid arithmetic oracle fixture", raw)
				}
				if isIntegralNumber(raw) != number.IsInt() {
					t.Fatal("decimal-position result differs from exact arithmetic", raw, number)
				}
			}
		}
	}
}

func TestIntegralNumberEvaluationBoundaries(t *testing.T) {
	read := readExpression("input", "")
	plan := numericTestPlan(t, Expression{Op: "is_integral", Source: &read})
	for _, boundary := range []string{"steps", "bytes", "cancel"} {
		state := numericTestState(t, numericTestInput(t, "9007199254740993.0"))
		switch boundary {
		case "steps":
			state.budget.stepLimit = 1
		case "bytes":
			state.budget.limits.BufferBytes = 2
		case "cancel":
			state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 2}
		}
		_, err := plan.evaluate(state)
		var failure *ResourceLimitError
		if boundary == "cancel" {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("predicate bypassed source cancellation", err)
			}
		} else if !errors.As(err, &failure) || failure.Metric != boundary {
			t.Fatal("predicate bypassed its evaluation budget", boundary, err)
		}
		if state.budget.steps != 2 {
			t.Fatal("predicate changed expression charging", boundary, state.budget.steps)
		}
	}
	for _, source := range []Expression{read, literalExpression(1)} {
		compiler := newExpressionCompiler(DefaultLimits(), nil)
		if _, err := compiler.compilePlan(Expression{Op: "is_integral", Source: &source}, "/integral", expressionScope{}, 1); err != nil {
			t.Fatal(err)
		}
	}
	compiler := newExpressionCompiler(DefaultLimits(), nil)
	if _, err := compiler.compilePlan(Expression{Op: "is_integral"}, "/integral", expressionScope{}, 1); err == nil {
		t.Fatal("missing source compiled")
	}
}

func TestIntegralNumberCatalogReferencesAndPermissions(t *testing.T) {
	operations := DescribeSchema().Operations
	index := slices.IndexFunc(operations, func(operation OperationInfo) bool { return operation.Name == "is_integral" })
	if index < 0 || operations[index].Result != BooleanType || !slices.Equal(operations[index].Fields, []string{"source"}) || !slices.Equal(operations[index].Required, []string{"source"}) {
		t.Fatal("catalog lost the numeric predicate contract")
	}
	read := readExpression("input", "")
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{"integral": {Op: "is_integral", Source: &read}})
	call := Expression{Op: "ref", Ref: "integral"}
	first, err := compiler.compilePlan(call, "/first", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiler.compilePlan(call, "/second", expressionScope{bindingDepth: 3}, 1)
	if err != nil || first != second {
		t.Fatal("pure numeric reference lost lexical sharing", err)
	}
	compiler.releaseGraph()
	value, err := first.evaluate(numericTestState(t, numericTestInput(t, "1.00e1")))
	if err != nil || value != coreBoolean(true) {
		t.Fatal("predicate retained compilation-only state", value, err)
	}
	for _, path := range []string{"/role", "/attributes/wire:private"} {
		source := readExpression("input", path)
		compiled := compileTestOutputExpression(t, Expression{Op: "is_integral", Source: &source})
		err := checkScopeExpression(compiled, make(map[*compiledExpression]bool))
		if (err == nil) != (path == "/role") {
			t.Fatal("numeric predicate changed scope read permissions", path, err)
		}
	}
}
