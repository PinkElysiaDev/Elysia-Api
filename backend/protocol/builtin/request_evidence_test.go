package builtin

import (
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func verifyRequestEvidence(t *testing.T, source, target *p.Compiled, id, body string, capabilities p.CapabilitySet) p.CombinationReport {
	t.Helper()
	request, err := source.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	// Verification compares semantics after removing transport provenance.
	request.Native, request.Source = nil, p.Identity{}
	clearEvidenceProvenance(request.Content)
	for index := range request.Tools {
		request.Tools[index].Native = nil
	}
	expected, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := source.Package()
	var required []p.Capability
	for capability, enabled := range capabilities {
		if enabled {
			required = append(required, capability)
		}
	}
	slices.Sort(required)
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{ID: id, Direction: p.DecodeRequest, Input: testValue(t, body), Expected: expected, Scope: p.Scope{Model: "m"}, Capabilities: required})
	withSample, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	report := p.VerifyBindingCombination(t.Context(), withSample, target, capabilities)
	for _, check := range report.Checks {
		if check.SampleID == id {
			if !check.Passed || check.Skipped {
				t.Fatal("request history evidence failed", check, report.Issues)
			}
			return report
		}
	}
	t.Fatal("request history was omitted from combination evidence", id)
	return report
}

func clearEvidenceProvenance(nodes []p.Node) {
	for index := range nodes {
		nodes[index].Native, nodes[index].Source = nil, nil
		clearEvidenceProvenance(nodes[index].Children)
		clearEvidenceProvenance(nodes[index].ReasoningContent)
	}
}
