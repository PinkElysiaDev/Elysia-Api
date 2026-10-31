package protocol

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestWireAssertionDiagnosticPreservesBoundary(t *testing.T) {
	for _, stage := range []string{"wire", "", "runtime", "stream", "request.validation"} {
		t.Run("stage="+stage, func(t *testing.T) {
			assertion := WireAssertion{Path: "/value", Schema: ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "expected a string"}
			if stage != "wire" {
				assertion.Diagnostic = &RejectDiagnostic{Code: InvalidInput, Stage: stage}
			}
			expectedPath, expectedDirection := "/value", Direction("")
			if stage == "stream" {
				expectedPath, expectedDirection = "/usage/details/tool", DecodeEvent
				assertion.Diagnostic.Path, assertion.Diagnostic.Direction = expectedPath, expectedDirection
			}
			contract, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}, Assertions: []WireAssertion{assertion}}, "/document", DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			err = contract.validator.ValidateWireOutput(DecodeResponse, parseTestValue(t, `{"value":42}`))
			var failure *ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 || errors.Unwrap(failure) == nil {
				t.Fatal("assertion lost its structured cause", err)
			}
			issue := failure.Issues[0]
			if issue.Code != InvalidInput || issue.Stage != stage || issue.Path != expectedPath || issue.Direction != expectedDirection || issue.Reason != assertion.Reason {
				t.Fatal("assertion changed its declared boundary", issue)
			}
			if err := contract.validator.ValidateWireOutput(DecodeResponse, parseTestValue(t, `{"value":"ok"}`)); err != nil {
				t.Fatal("diagnostic changed successful validation", err)
			}
		})
	}
	for _, diagnostic := range []RejectDiagnostic{{Code: InvalidInput, Stage: "routing"}, {Code: ExecutionCancelled}, {Code: InvalidInput, Direction: "invented"}, {Code: InvalidInput, Path: "value"}} {
		_, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: ObjectType}, Assertions: []WireAssertion{{Path: "/value", Schema: ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "expected a string", Diagnostic: &diagnostic}}}, "/document", DefaultLimits())
		if err == nil {
			t.Fatal("unrelated classification was accepted", diagnostic)
		}
	}
}

func TestWireAssertionReasonUsesFailedContainer(t *testing.T) {
	reason := Expression{Op: "join", Items: []Expression{
		readExpression("root", "/label"), literalExpression(" output event "),
		{Op: "quote_string", Source: expressionPointer(readExpression("input", "/type"))},
	}}
	assertion := WireAssertion{Path: "/value", Schema: ValueSchema{Type: StringType}, Code: ConversionRejected,
		ReasonExpression: &reason, Diagnostic: &RejectDiagnostic{Code: ConversionRejected, Stage: "wire.output"}}
	contract, err := compileWireContractSpec(WireContractSpec{
		Schema:     &ValueSchema{Type: ObjectType, AllowUnknown: true},
		Assertions: []WireAssertion{{Path: "/items", Schema: ValueSchema{Type: ArrayType}, Code: ConversionRejected, Reason: "items must be an array", Each: []WireAssertion{assertion}}},
	}, "/document", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Missing label/type would fail the reason expression. A passing assertion
	// must not read them or build a diagnostic.
	if err := contract.validator.ValidateWireOutput(EncodeEvent, parseTestValue(t, `{"items":[{"value":"ok"}]}`)); err != nil {
		t.Fatal("successful assertion evaluated its diagnostic", err)
	}
	for _, kind := range []string{"unknown", "quote\"\nnext\t<&\u2028"} {
		wire := fixtureValue(t, map[string]any{"label": "fixture", "items": []any{map[string]any{"type": kind, "value": false}}})
		err := contract.validator.ValidateWireOutput(EncodeEvent, wire)
		var failure *ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || errors.Unwrap(failure) == nil {
			t.Fatal("dynamic diagnostic lost its original schema failure", err)
		}
		issue := failure.Issues[0]
		if issue.Code != ConversionRejected || issue.Stage != "wire.output" || issue.Path != "/items/0/value" || issue.Reason != fmt.Sprintf("fixture output event %q", kind) {
			t.Fatal("dynamic diagnostic changed its container, root, quoted value or classification", issue)
		}
	}
	validator := contract.validator.(*specWireValidator)
	compiledAssertion := validator.assertions[0].each[0]
	cause := errors.New("original validation failure")
	state := evaluation{ctx: t.Context(), input: parseTestValue(t, `{}`), root: parseTestValue(t, `{"label":"fixture"}`), budget: &evaluationBudget{limits: DefaultLimits()}}
	if err := compiledAssertion.failure(state, "/items/0/value", cause); !errors.Is(err, cause) {
		t.Fatal("failed diagnostic expression discarded the original cause", err)
	}
	if err := compiledAssertion.failure(state, "/items/0/value", context.Canceled); err != context.Canceled {
		t.Fatal("diagnostic expression replaced cancellation", err)
	}
}

func TestWireAssertionRejectsInvalidReasonExpressions(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		reason     string
		expression Expression
	}{
		{"ambiguous", "static reason", literalExpression("dynamic reason")},
		{"nonstring", "", literalExpression(42)},
		{"unknown-result", "", readExpression("input", "/unknown")},
		{"external-context", "", Expression{Op: "quote_string", Source: expressionPointer(readExpression("context", "/value"))}},
		{"stream-state", "", Expression{Op: "quote_string", Source: expressionPointer(readExpression("state", "/value"))}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			spec := WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}, Assertions: []WireAssertion{{
				Path: "/value", Schema: ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: fixture.reason, ReasonExpression: &fixture.expression,
			}}}
			if _, err := compileWireContractSpec(spec, "/document", DefaultLimits()); err == nil {
				t.Fatal("invalid or nonlocal diagnostic expression compiled")
			}
		})
	}
}

func TestWireAssertionCheckKeepsExplicitRejection(t *testing.T) {
	rejection := Expression{Op: "reject", Source: expressionPointer(readExpression("input", "/location")),
		Reason: "association is incomplete", Diagnostic: &RejectDiagnostic{Code: InvalidAssociation, Stage: "wire.output"}}
	check := Expression{Op: "if", When: expressionPointer(readExpression("input", "/reject")), Then: &rejection, Otherwise: expressionPointer(readExpression("input", "/valid"))}
	contract, err := compileWireContractSpec(WireContractSpec{
		Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true},
		Assertions: []WireAssertion{{Path: "", Schema: ValueSchema{Type: ObjectType, AllowUnknown: true},
			Code: ConversionRejected, Reason: "invalid document", Check: &check}},
	}, "/document", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		raw, stage, path, reason string
		code                     IssueCode
	}{
		{`{"reject":true,"location":"/messages/7/tool_calls"}`, "wire.output", "/messages/7/tool_calls", rejection.Reason, InvalidAssociation},
		{`{"reject":false,"valid":false}`, "wire", "", "invalid document", ConversionRejected},
		{`{"reject":false,"valid":true}`, "", "", "", ""},
	} {
		err := contract.validator.ValidateWireOutput(EncodeRequest, parseTestValue(t, fixture.raw))
		if fixture.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var failure *ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 {
			t.Fatal("check lost its classification", err)
		}
		issue := failure.Issues[0]
		if issue.Code != fixture.code || issue.Stage != fixture.stage || issue.Path != fixture.path || issue.Reason != fixture.reason {
			t.Fatal("check replaced the selected branch diagnostic", issue)
		}
		var declared *declaredRejectionError
		if fixture.code == InvalidAssociation && !errors.As(err, &declared) {
			t.Fatal("check discarded its original rejection cause", err)
		}
	}
}
