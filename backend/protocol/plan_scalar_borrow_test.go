package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBorrowedScalarReadBoundaries(t *testing.T) {
	read := Expression{Op: "read", Path: "/value", Required: true}
	for _, operation := range []string{"exists", "present", "type", "is_integer", "is_integral", "equal"} {
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

func TestScalarBorrowFailureLeavesCallerReader(t *testing.T) {
	input := numericTestInput(t, `{"text":"small","padding":"`+strings.Repeat("x", requestViewMinBytes*2)+`"}`)
	for _, operation := range []string{"present", "type", "is_integer", "is_integral", "equal"} {
		for _, shouldCancel := range []bool{false, true} {
			read := Expression{Op: "read", From: "input", Path: "/missing", Required: true}
			expression := Expression{Op: operation, Source: &read}
			if operation == "equal" {
				expression.Source = nil
				expression.Items = []Expression{read, literalExpression("known")}
			}
			plan := numericTestPlan(t, expression)
			view := newInputView(input)
			state := numericTestState(t, input)
			state.inputView, state.valueReader, state.objectReader = view, &view.owned, view.objectReader()
			if shouldCancel {
				state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 2}
			}
			_, err := plan.buildNode(&state)
			if err == nil || state.budget.steps != 2 || state.valueReader != &view.owned || view.owned.isBorrowed {
				t.Fatal("failed borrowed source changed caller state", operation, shouldCancel, err, state.budget.steps)
			}
			if shouldCancel != errors.Is(err, context.Canceled) {
				t.Fatal("borrowed source changed failure precedence", operation, shouldCancel, err)
			}
			state.ctx = t.Context()
			value, err := readValuePointerWithObjectReader(input, []string{"text"}, state.valueReader, state.objectReader)
			if err != nil || !value.IsString("small") || sharesValueStorage(input, value) {
				t.Fatal("later retained read borrowed the input after failure", operation, shouldCancel, err)
			}
		}
	}
}
