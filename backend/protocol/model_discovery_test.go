package protocol

import "testing"

func discoveryModelSamples(t *testing.T) []ModelSample {
	t.Helper()
	var samples []ModelSample
	for index, page := range []ModelPage{{Models: []DiscoveredModel{}}, {Models: []DiscoveredModel{{ID: "m", MaxTokens: 1000000}}, Next: "next"}} {
		value := fixtureValue(t, page)
		samples = append(samples, ModelSample{ID: []string{"empty", "continued"}[index], Operation: "models", Input: value, Expected: value})
	}
	return samples
}

func TestDiscoveryVerificationRequiresPaginationEvidence(t *testing.T) {
	document := discoveryPackage(t)
	compiled := compilePackageForTest(t, document)
	if report := Verify(t.Context(), compiled); !report.Passed { t.Fatal(report.Issues) }
	document.Mapping.ModelSamples = document.Mapping.ModelSamples[:1]
	missing := compilePackageForTest(t, document)
	if report := Verify(t.Context(), missing); report.Passed { t.Fatal("pagination enabled without continuation evidence") }
	if compiled.SamplesHash() == missing.SamplesHash() { t.Fatal("discovery evidence excluded from samples hash") }
	copy, _ := compiled.Package()
	copy.Mapping.Expressions["catalog-page"] = readExpression("input", "/changed")
	page, err := compiled.DecodeModelPage(t.Context(), "models", fixtureValue(t, ModelPage{Models: []DiscoveredModel{{ID: "m"}}}))
	if err != nil || len(page.Models) != 1 { t.Fatal("package copy mutated compiled mapping", page, err) }
}

func TestDiscoveryRejectsMalformedCatalogValues(t *testing.T) {
	compiled := compilePackageForTest(t, discoveryPackage(t))
	for _, raw := range []string{`{}`, `{"models":null}`, `{"models":[{"id":null}]}`, `{"models":[{"id":""}]}`, `{"models":[{"id":"a"},{"id":"a"}]}`, `{"models":[{"id":"a","maxTokens":1.5}]}`, `{"models":[{"id":"a","maxTokens":-1}]}`, `{"models":[],"next":42}`, `{"models":[],"next":true}`, `{"models":[],"next":[]}`, `{"models":[],"next":{}}`, `{"models":[],"hasMore":true}`, `{"models":[],"hasMore":true,"next":""}`, `{"models":[],"hasMore":true,"next":null}`, `{"models":[],"url":"https://other.invalid"}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := compiled.DecodeModelPage(t.Context(), "models", parseTestValue(t, raw)); err == nil { t.Fatal("malformed catalog accepted") }
		})
	}
}

func TestDiscoveryCompilerRejectsCredentialAndMappingConflicts(t *testing.T) {
	for _, change := range []func(*ProtocolOperationSpec){
		func(operation *ProtocolOperationSpec) { operation.Method = "POST" },
		func(operation *ProtocolOperationSpec) { operation.Models = nil },
		func(operation *ProtocolOperationSpec) { operation.Models.CursorParameter = "api_key" },
		func(operation *ProtocolOperationSpec) { operation.Query = map[string]string{"cursor": "static"} },
		func(operation *ProtocolOperationSpec) { operation.Auth = Credential{Location: "query", Name: "cursor"} },
	} {
		document := discoveryPackage(t)
		operation := document.Spec.Operations["models"]
		change(&operation)
		document.Spec.Operations["models"] = operation
		compiler, _ := NewCompiler(DefaultLimits(), nil)
		if _, issues := compiler.Compile(fixtureValue(t, document).Bytes()); IssuesError(issues) == nil { t.Fatal("invalid discovery operation compiled") }
	}
}

func TestDiscoveryNormalizesTerminalCursors(t *testing.T) {
	compiled := compilePackageForTest(t, discoveryPackage(t))
	for _, raw := range []string{`{"models":[]}`, `{"models":[],"next":null}`, `{"models":[],"next":""}`} {
		page, err := compiled.DecodeModelPage(t.Context(), "models", parseTestValue(t, raw))
		if err != nil || page.Next != "" { t.Fatalf("terminal cursor: %s -> %+v (%v)", raw, page, err) }
	}
}
