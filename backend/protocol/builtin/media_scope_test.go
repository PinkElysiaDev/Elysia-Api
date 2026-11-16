package builtin

import (
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const mediaScopeSampleID = "image-data-uri-decode"

func clearMediaScopeNative(nodes []p.Node) {
	for index := range nodes {
		nodes[index].Native = nil
		clearMediaScopeNative(nodes[index].Children)
		clearMediaScopeNative(nodes[index].ReasoningContent)
	}
}

func TestMediaScopeDataURIInverseWithoutNative(t *testing.T) {
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			compiled := testCompiled(t, name)
			samples := packageMapping(compiled).Samples
			index := slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == mediaScopeSampleID })
			if index < 0 {
				t.Fatal("missing authored data URI decode fixture")
			}
			sample := samples[index]
			options := fixtureOptions(p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
			request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
			if err != nil {
				t.Fatal(err)
			}
			images := fixtureNodes(request.Content, p.ImageNode)
			if len(images) != 1 {
				t.Fatal("data URI did not decode to one core image", images)
			}
			sameJSON(t, images[0].Payload.Bytes(), mediaDetailPayload)
			request.Native = nil
			clearMediaScopeNative(request.Content)
			var editImages func([]p.Node)
			editImages = func(nodes []p.Node) {
				for index := range nodes {
					if nodes[index].Kind == p.ImageNode {
						nodes[index].Payload = testValue(t, `{"mime":"image/jpeg","data":"Yg=="}`)
					}
					editImages(nodes[index].Children)
					editImages(nodes[index].ReasoningContent)
				}
			}
			editImages(request.Content)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), prepared, options)
			if err != nil {
				t.Fatal("same-source mapping could not rebuild edited core image", err)
			}
			if !strings.Contains(string(wire), "data:image/jpeg;base64,Yg==") || strings.Contains(string(wire), "data:image/png;base64,YQ==") {
				t.Fatal("edited image was replaced with original Native bytes", string(wire))
			}
			decoded, err := compiled.DecodeRequest(t.Context(), wire, options)
			if err != nil {
				t.Fatal(err)
			}
			images = fixtureNodes(decoded.Content, p.ImageNode)
			if len(images) != 1 {
				t.Fatal("rebuilt image changed cardinality", images)
			}
			sameJSON(t, images[0].Payload.Bytes(), `{"mime":"image/jpeg","data":"Yg=="}`)
		})
	}
}

func TestMediaRequestScopesKeepFullFailures(t *testing.T) {
	target := testCompiled(t, Gemini)
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.ImagesCapability: true}
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			source := testCompiled(t, name)
			reports := p.VerifyBindingProfiles(t.Context(), source, target, contract)
			if len(reports) == 0 || reports[0].Passed || reports[0].RequestScope != nil || reports[0].IsRestricted {
				t.Fatal("original complete failure was replaced", reports)
			}
			hasOriginalFailure := slices.ContainsFunc(reports[0].Checks, func(check p.VerificationCheck) bool {
				return check.SampleID == "image-decode" && !check.Passed && !check.Skipped && !check.Rejected
			})
			if !hasOriginalFailure {
				t.Fatal("original MIME-less URL failure disappeared", reports[0])
			}
			for _, transport := range []p.Transport{p.HTTPJSON, p.SSE} {
				hasScopedProof := false
				for _, report := range reports {
					if report.RequestScope == nil || report.Transport != transport || !report.Passed || !report.Capabilities[p.ImagesCapability] {
						continue
					}
					if !report.IsRestricted || report.BindingHash != p.CapabilityContractHash(contract) || report.RequestScope.OwnerRevision != target.Hash() || report.RequestScope.Boundary != p.EncodeRequest {
						t.Fatal("scoped report lost immutable ownership or binding identity", report)
					}
					hasImage := slices.ContainsFunc(report.Checks, func(check p.VerificationCheck) bool {
						return check.SampleID == mediaScopeSampleID && check.Direction == p.EncodeRequest && check.Passed && !check.Skipped && !check.Rejected && slices.Contains(check.Capabilities, p.ImagesCapability)
					})
					hasExcludedURL := slices.ContainsFunc(report.Checks, func(check p.VerificationCheck) bool {
						return check.SampleID == "image-decode" && check.Direction == p.EncodeRequest && check.Skipped && !check.Passed && strings.Contains(check.Reason, report.RequestScope.ID) && check.Path != ""
					})
					if !hasImage || !hasExcludedURL {
						t.Fatal("scope has no real positive image request or lost URL exclusion", report)
					}
					hasScopedProof = true
				}
				if !hasScopedProof {
					t.Fatal("missing independently replayed image transport", transport, reports)
				}
			}
		})
	}
}

func TestMediaScopeRequiresPositiveRequestImage(t *testing.T) {
	target := testCompiled(t, Gemini)
	document, _ := target.Package()
	kept := document.Mapping.Samples[:0]
	for _, sample := range document.Mapping.Samples {
		if sample.Direction == p.DecodeRequest && sample.ExpectedIssue == "" {
			var request p.Request
			if err := sample.Expected.Decode(&request); err != nil {
				t.Fatal(err)
			}
			if len(fixtureNodes(request.Content, p.ImageNode)) != 0 {
				continue
			}
		}
		kept = append(kept, sample)
	}
	document.Mapping.Samples = kept
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	source, issues := compiler.CompilePackage(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.ImagesCapability: true}
	reports := p.VerifyBindingProfiles(t.Context(), source, source, contract)
	hasScopedCoverageFailure := false
	for _, report := range reports {
		if report.RequestScope == nil {
			continue
		}
		if report.Passed {
			t.Fatal("text requests or image responses supplied request-image proof", report)
		}
		for _, issue := range report.Issues {
			hasScopedCoverageFailure = hasScopedCoverageFailure || issue.Code == p.IncompleteCoverage && issue.Direction == p.EncodeRequest && strings.Contains(issue.Path, "/verificationScopes/")
		}
	}
	if !hasScopedCoverageFailure {
		t.Fatal("missing image requests were not diagnosed as scope coverage failure", reports)
	}
}
