package protocol

import (
	"errors"
	"testing"
)

func TestWireAssertionDiagnosticPreservesBoundary(t *testing.T) {
	for _, stage := range []string{"wire", "", "runtime", "stream"} {
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
