package protocol

import "testing"

func TestDiagnosticDeduplicationPreservesDistinctOutcomes(t *testing.T) {
	base := ConversionIssue{Code: ConversionDegraded, Path: "/include", Severity: SeverityInfo, Fidelity: "preserved", Stage: "conversion.request", RuleID: "responses-include", PolicyHash: "hash", PolicyRevision: "rev", Protocol: Identity{DefinitionID: "responses", Revision: "one"}, Reason: "reason"}
	changes := []func(*ConversionIssue){
		func(i *ConversionIssue) { i.Code = ConversionNormalized },
		func(i *ConversionIssue) { i.Stage = "conversion.response" },
		func(i *ConversionIssue) { i.Severity = SeverityError },
		func(i *ConversionIssue) { i.Fidelity = "lossy_compatible" },
		func(i *ConversionIssue) { i.RuleID = "other-rule" },
		func(i *ConversionIssue) { i.PolicyHash = "other-policy" },
		func(i *ConversionIssue) { i.PolicyRevision = "other-revision" },
		func(i *ConversionIssue) { i.Protocol.Revision = "two" },
		func(i *ConversionIssue) { i.Protocol.DefinitionID = "custom-copy" },
	}
	sink := &DiagnosticSink{}
	sink.Add(base)
	for _, change := range changes {
		issue := base
		change(&issue)
		sink.Add(issue)
		sink.Add(issue)
	}
	sink.Add(base)
	if len(sink.Issues()) != len(changes)+1 {
		t.Fatal(sink.Issues())
	}
}
