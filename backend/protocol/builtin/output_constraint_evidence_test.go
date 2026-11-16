package builtin

import (
	"context"
	"errors"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const outputSchemaRuleID = "output-constraint-schema"

func compileOutputConstraintPolicy(t *testing.T, source, target *p.Compiled, mode string, isSchemaRuleEnabled bool) *p.CompiledConversion {
	t.Helper()
	policy := p.DefaultConversionPolicy(source, target)
	policy.Mode = mode
	index := slices.IndexFunc(policy.Rules, func(rule p.ConversionRule) bool { return rule.ID == outputSchemaRuleID })
	if index < 0 {
		t.Fatal("missing target-owned schema boundary")
	}
	policy.Rules[index].Enabled = isSchemaRuleEnabled
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	return conversion
}

func requireOutputSchemaNegativeEvidence(t *testing.T, report p.CombinationReport) {
	t.Helper()
	index := slices.IndexFunc(report.Checks, func(check p.VerificationCheck) bool { return check.SampleID == "output-string-enum-decode" })
	if index < 0 {
		t.Fatal("string enum fixture disappeared from paired evidence", report.Checks)
	}
	check := report.Checks[index]
	if !check.Rejected || check.Passed || check.Skipped || len(check.Capabilities) != 0 {
		t.Fatal("declared schema refusal granted positive capability coverage", check)
	}
}

func TestOutputConstraintDeclaredSchemaRefusalEvidence(t *testing.T) {
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.OutputConstraintsCapability: true}
	target := testCompiled(t, Anthropic)
	for _, codec := range []string{Chat, Responses, Gemini} {
		t.Run(codec, func(t *testing.T) {
			source := testCompiled(t, codec)
			conversion := compileOutputConstraintPolicy(t, source, target, "compatible", true)
			report := p.VerifyBindingCombination(t.Context(), source, target, contract, conversion)
			if !report.Passed || report.PolicyHash != conversion.Hash || report.BindingHash != p.CapabilityContractHash(contract) {
				t.Fatal("supported output constraints lost their pinned combination", report.Issues)
			}
			requireOutputSchemaNegativeEvidence(t, report)
			for _, sampleID := range []string{"output-constraint-decode", "output-numeric-enum-decode"} {
				if !slices.ContainsFunc(report.Checks, func(check p.VerificationCheck) bool {
					return check.SampleID == sampleID && check.Passed && !check.Rejected && slices.Contains(check.Capabilities, p.OutputConstraintsCapability)
				}) {
					t.Fatal("supported schema sample no longer supplies its own positive evidence", sampleID)
				}
			}
			disabled := compileOutputConstraintPolicy(t, source, target, "compatible", false)
			failed := p.VerifyBindingCombination(t.Context(), source, target, contract, disabled)
			if failed.Passed || failed.PolicyHash == report.PolicyHash || !slices.ContainsFunc(failed.Checks, func(check p.VerificationCheck) bool {
				return check.SampleID == "output-string-enum-decode" && !check.Passed && !check.Rejected && !check.Skipped
			}) {
				t.Fatal("disabled refusal reused an enabled rule's evidence", failed)
			}
			changed := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.OutputConstraintsCapability: true, p.ImagesCapability: false}
			if target.HasRequestScopeEvidence(source, changed, []p.CombinationReport{report}) {
				t.Fatal("changed binding reused stale schema evidence")
			}
		})
	}
}

func TestOutputConstraintRefusalNeedsPositiveCoverage(t *testing.T) {
	source, target := testCompiled(t, Chat), testCompiled(t, Anthropic)
	document, _ := source.Package()
	// This test-local package intentionally lacks positive output coverage;
	// the shipped samples remain intact in the real package and gateway tests.
	document.Mapping.Samples = slices.DeleteFunc(document.Mapping.Samples, func(sample p.Sample) bool {
		return sample.Direction == p.DecodeRequest && sample.ID != "output-string-enum-decode" && slices.Contains(sample.Capabilities, p.OutputConstraintsCapability)
	})
	withoutPositive, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.OutputConstraintsCapability: true}
	report := p.VerifyBindingCombination(t.Context(), withoutPositive, target, contract)
	requireOutputSchemaNegativeEvidence(t, report)
	if report.Passed || !slices.ContainsFunc(report.Issues, func(issue p.ConversionIssue) bool {
		return issue.Code == p.IncompleteCoverage && issue.Path == "/binding/capabilities/output.constraints"
	}) {
		t.Fatal("negative schema evidence authorized a capability without a positive sample", report)
	}
}

func TestOutputConstraintRefusalPolicyBoundaries(t *testing.T) {
	source, request := decodeOutputEnumFixture(t, Chat, outputStringEnumSchema)
	target := testCompiled(t, Anthropic)
	path := outputConstraintCorePath + "/schema/properties/answer/enum"
	for _, mode := range []string{"compatible", "strict"} {
		t.Run(mode, func(t *testing.T) {
			conversion := compileOutputConstraintPolicy(t, source, target, mode, true)
			before, err := p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
			requireOutputConstraintIssue(t, err, path)
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal("schema refusal lacks a single attributed diagnostic", err)
			}
			issue := failure.Issues[0]
			if issue.Code != p.ConversionRejected || issue.RuleID != outputSchemaRuleID || issue.PolicyHash != conversion.Hash || issue.Fidelity != "rejected" || issue.Stage != "conversion.request" {
				t.Fatal("schema refusal lost its target policy identity", issue)
			}
			after, err := p.EncodeValue(request)
			if err != nil || before != after {
				t.Fatal("schema refusal mutated the caller's request", err)
			}
		})
	}
	disabled := compileOutputConstraintPolicy(t, source, target, "compatible", false)
	prepared, err := disabled.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
	requireOutputConstraintIssue(t, err, path)
	_, direct := decodeOutputConstraintFixture(t, Anthropic)
	setOutputConstraintSchema(t, direct, outputStringEnumSchema)
	_, err = target.EncodeRequest(t.Context(), direct, fixtureOptions(p.EvaluationContext{}))
	requireOutputConstraintIssue(t, err, path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	conversion := compileOutputConstraintPolicy(t, source, target, "compatible", true)
	_, err = conversion.Request(ctx, request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled evaluation became declared schema evidence", err)
	}
}
