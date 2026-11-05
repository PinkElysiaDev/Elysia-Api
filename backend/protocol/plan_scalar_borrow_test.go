package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestBorrowedScalarReadBoundaries(t *testing.T) {
	read := Expression{Op: "read", Path: "/value", Required: true}
	for _, operation := range []string{"exists", "present", "type", "is_integer", "equal"} {
		expression := Expression{Op: operation, Source: &read}
		if operation == "equal" {
			expression.Source = nil
			expression.Items = []Expression{read, literalExpression("value")}
		}
		plan := numericTestPlan(t, expression)
		if !plan.borrowsScalarInput {
			t.Fatal("predicate did not select its borrowed read", operation)
		}
		original := *plan
		original.borrowsScalarInput = false
		for _, raw := range []string{`{}`, `{"value":null}`, `{"value":9007199254740993}`, `{"value":"<>&😀"}`, `{"value":"` + strings.Repeat("x", 1<<16) + `"}`} {
			for _, boundary := range []string{"normal", "bytes", "steps", "cancel"} {
				run := func(current *compiledOutputPlan) (Value, int, error) {
					state := numericTestState(t, numericTestInput(t, raw))
					switch boundary {
					case "bytes":
						state.budget.limits.BufferBytes = 2
					case "steps":
						state.budget.stepLimit = 1
					case "cancel":
						state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 2}
					}
					output, err := current.evaluate(state)
					return output, state.budget.steps, err
				}
				want, wantSteps, wantErr := run(&original)
				got, gotSteps, gotErr := run(plan)
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || gotSteps != wantSteps {
					t.Fatal("borrowed predicate changed result/error/accounting", operation, boundary, got, want, gotErr, wantErr, gotSteps, wantSteps)
				}
			}
		}
	}
}

func TestBorrowedPredicateDoesNotChangeEscapingRead(t *testing.T) {
	read := Expression{Op: "read", Path: "/value"}
	expression := Expression{Op: "object", Fields: map[string]Expression{
		"present": {Op: "present", Source: &read}, "value": read,
	}}
	plan := numericTestPlan(t, expression)
	input := numericTestInput(t, `{"value":"small","padding":"`+strings.Repeat("x", 1<<20)+`"}`)
	state := numericTestState(t, input)
	view := newInputView(input)
	state.inputView, state.valueReader, state.objectReader = view, &view.owned, view.objectReader()
	output, err := plan.build(state)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := readOutputPointer(output.output, []string{"value"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	borrowed, err := scanObjectMember(input, "value")
	if err != nil {
		t.Fatal(err)
	}
	if selected.value != borrowed || spanIdentity(selected.value.raw) == spanIdentity(borrowed.raw) || view.owned.isBorrowed {
		t.Fatal("predicate let an escaping field borrow the original body")
	}
}
