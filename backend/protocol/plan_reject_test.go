package protocol

import (
	"errors"
	"testing"
)

func TestPackageRejectBranchKeepsDeclaredBoundary(t *testing.T) {
	document := textProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeRequest]
	original := entry.Program
	entry.Program = Expression{Op: "if", When: expressionPointer(readExpression("input", "/parameters/reject")),
		Then: &Expression{Op: "reject", Path: "/error/code", Reason: "declared code rejection", Diagnostic: &RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire"}}, Otherwise: &original}
	document.Mapping.Directions[EncodeRequest] = entry
	compiled := compilePackageForTest(t, document)
	for _, shouldReject := range []bool{false, true} {
		request := Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Parameters: Object{"reject": fixtureValue(t, shouldReject)},
			Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}}
		output, err := compiled.EncodeRequestValue(t.Context(), &request, EvaluationContext{})
		if !shouldReject {
			if err != nil || output.IsZero() {
				t.Fatal("unselected rejection changed normal mapping", err)
			}
			continue
		}
		var failure *ConversionError
		var declared *declaredRejectionError
		if !errors.As(err, &failure) || !errors.As(err, &declared) || len(failure.Issues) != 1 {
			t.Fatal("declarative rejection lost its error chain", err)
		}
		issue := failure.Issues[0]
		if issue.Code != UnsupportedCapability || issue.Stage != "wire" || issue.Path != "/error/code" || issue.Reason != "declared code rejection" || issue.Direction != EncodeRequest {
			t.Fatal("declared rejection was reclassified", issue)
		}
	}
}
