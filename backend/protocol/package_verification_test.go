package protocol

import (
	"strings"
	"testing"
)

func verifiedTextPackage(t *testing.T, id string) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	document.Spec.ID, document.Spec.Name, document.Spec.Family = id, id, id
	request := Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}}
	response := Response{SchemaVersion: ProtocolCoreVersion, Status: StringValue("completed"), Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: TextNode, Payload: StringValue("answer")}}}}}
	requestWire := Value{raw: `{"model":"m","prompt":"hello"}`}
	responseWire := Value{raw: `{"reply":"answer"}`}
	requestCore, responseCore := fixtureValue(t, request), fixtureValue(t, response)
	document.Mapping.Samples = []Sample{
		{ID: "request.decode", Direction: DecodeRequest, Input: requestWire, Expected: requestCore},
		{ID: "request.encode", Direction: EncodeRequest, Input: requestCore, Expected: requestWire},
		{ID: "response.decode", Direction: DecodeResponse, Input: responseWire, Expected: responseCore},
		{ID: "response.encode", Direction: EncodeResponse, Input: responseCore, Expected: responseWire},
	}
	return document
}

func TestPackageVerificationUsesRuntimeAndBindsEvidence(t *testing.T) {
	document := verifiedTextPackage(t, "verified-package")
	compiled := compilePackageForTest(t, document)
	report := Verify(t.Context(), compiled)
	if !report.Passed || len(report.Checks) != 4 || len(report.Covered) != 1 || report.Covered[0] != TextCapability {
		t.Fatal("package samples did not traverse the existing verification pipeline", report)
	}
	if err := IssuesError(CanActivate(compiled, report)); err != nil {
		t.Fatal(err)
	}
	if !report.IsCurrent(compiled.Hash(), CompilerVersion, compiled.SamplesHash()) || report.Kind != OfflineVerification {
		t.Fatal("package evidence lacks immutable identity binding", report)
	}
	samples := compiled.verificationSamples()
	samples[0].ID = "caller-edit"
	if compiled.verificationSamples()[0].ID != document.Mapping.Samples[0].ID {
		t.Fatal("fixture accessor exposed the compiled fixture set")
	}
	document.Mapping.Samples[0].ID = "renamed"
	changed := compilePackageForTest(t, document)
	if compiled.SamplesHash() == changed.SamplesHash() || compiled.Hash() == changed.Hash() || IssuesError(CanActivate(changed, report)) == nil {
		t.Fatal("sample changes reused old passing evidence")
	}
	for _, alteration := range []string{"compiler", "samples", "kind"} {
		stale := report
		switch alteration {
		case "compiler":
			stale.CompilerVersion = "previous-compiler"
		case "samples":
			stale.SamplesHash = "previous-samples"
		case "kind":
			stale.Kind = UpstreamVerification
		}
		if IssuesError(CanActivate(compiled, stale)) == nil {
			t.Fatal("invalid package evidence authorized activation", alteration)
		}
	}
	if _, err := DecodeRequestContract(document.Mapping.Samples[1].Input.Bytes()); err == nil {
		t.Fatal("public legacy request parser silently accepted core2")
	}
}

func TestPackageVerificationRejectsMissingOrIncorrectEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   IssueCode
		change func(*ProtocolPackage)
	}{
		{"empty", IncompleteCoverage, func(p *ProtocolPackage) { p.Mapping.Samples = nil }},
		{"negative-only", IncompleteCoverage, func(p *ProtocolPackage) {
			p.Mapping.Samples = []Sample{{ID: "invalid-request", Direction: DecodeRequest, Input: Value{raw: `{"model":"m","prompt":false}`}, ExpectedIssue: InvalidInput}}
		}},
		{"wrong-expected", VerificationMismatch, func(p *ProtocolPackage) {
			p.Mapping.Samples[1].Expected = Value{raw: `{"model":"m","prompt":"wrong"}`}
		}},
		{"legacy-input", VerificationMismatch, func(p *ProtocolPackage) {
			p.Mapping.Samples[1].Input.raw = strings.Replace(p.Mapping.Samples[1].Input.raw, `"schemaVersion":2`, `"schemaVersion":1`, 1)
		}},
		{"legacy-request-expected", InvalidInput, func(p *ProtocolPackage) {
			p.Mapping.Samples[0].Expected.raw = strings.Replace(p.Mapping.Samples[0].Expected.raw, `"schemaVersion":2`, `"schemaVersion":1`, 1)
		}},
		{"missing-response-version", InvalidInput, func(p *ProtocolPackage) {
			p.Mapping.Samples[2].Expected.raw = strings.Replace(p.Mapping.Samples[2].Expected.raw, `"schemaVersion":2,`, "", 1)
		}},
		{"unproved-tools", IncompleteCoverage, func(p *ProtocolPackage) {
			p.Mapping.Capabilities[FunctionToolsCapability] = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := verifiedTextPackage(t, "invalid-package-evidence")
			tc.change(&document)
			compiled := compilePackageForTest(t, document)
			report := Verify(t.Context(), compiled)
			if report.Passed || !containsVerificationCode(report, tc.code) || IssuesError(CanActivate(compiled, report)) == nil {
				t.Fatal("invalid package evidence passed", report)
			}
			if tc.name == "negative-only" && (len(report.Checks) != 1 || !report.Checks[0].Passed) {
				t.Fatal("expected diagnostic was not executed", report)
			}
			if tc.name == "wrong-expected" {
				hasExpectedPath := false
				for _, issue := range report.Issues {
					hasExpectedPath = hasExpectedPath || strings.HasPrefix(issue.Path, "/mapping/samples/1/expected")
				}
				if !hasExpectedPath {
					t.Fatal("package mismatch did not locate its authored fixture", report.Issues)
				}
			}
		})
	}
}

func TestPackageVerificationCombinationUsesPinnedFixtures(t *testing.T) {
	first := compilePackageForTest(t, verifiedTextPackage(t, "package-a"))
	document := verifiedTextPackage(t, "package-b")
	second := compilePackageForTest(t, document)
	for _, pair := range [][2]*Compiled{{first, second}, {second, first}} {
		report := VerifyBindingCombination(t.Context(), pair[0], pair[1], CapabilitySet{TextCapability: true})
		if !report.Passed || report.Fidelity != "preserved" || len(report.Checks) != 2 {
			t.Fatal("existing combination path did not execute package request and response fixtures", report)
		}
		if report.SourceSamplesHash != pair[0].SamplesHash() || report.TargetSamplesHash != pair[1].SamplesHash() || report.CompilerVersion != CompilerVersion || report.SourceHash != pair[0].Hash() || report.TargetHash != pair[1].Hash() {
			t.Fatal("combination evidence lost package identity", report)
		}
	}
	contract := document.Spec.WireContracts[EncodeRequest]
	contract.Assertions = []WireAssertion{{Path: "/prompt", Schema: ValueSchema{Type: StringType, Enum: []Value{StringValue("different")}}, Code: ConversionRejected, Reason: "synthetic final wire rejection"}}
	document.Spec.WireContracts[EncodeRequest] = contract
	restricted := compilePackageForTest(t, document)
	report := VerifyCombination(t.Context(), first, restricted)
	if report.Passed || report.Fidelity != "rejected" {
		t.Fatal("package combination bypassed final wire validation", report)
	}
	document.Mapping.Samples = nil
	empty := compilePackageForTest(t, document)
	report = VerifyCombination(t.Context(), empty, empty)
	if report.Passed || len(report.Checks) != 0 || len(report.Issues) == 0 {
		t.Fatal("empty package combination supplied positive evidence", report)
	}
}
