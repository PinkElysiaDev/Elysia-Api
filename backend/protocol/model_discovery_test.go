package protocol

import (
	"encoding/json"
	"testing"
)

func discoveryDefinition(t *testing.T) Definition {
	t.Helper()
	definition := textVerificationDefinition(t, "catalog")
	transform := readExpression("input", "")
	definition.Operations["models"] = Operation{Kind: "models", Method: "GET", Path: "/catalog", Transport: HTTPJSON, Auth: Credential{Location: "none"}, Models: &ModelDiscovery{Decode: Mapping{Transform: &transform}, CursorParameter: "cursor"}}
	for index, page := range []ModelPage{{Models: []DiscoveredModel{}}, {Models: []DiscoveredModel{{ID: "m", MaxTokens: 1000000}}, Next: "next"}} {
		value := fixtureValue(t, page)
		definition.ModelSamples = append(definition.ModelSamples, ModelSample{ID: []string{"empty", "continued"}[index], Operation: "models", Input: value, Expected: value})
	}
	return definition
}

func TestDiscoveryVerificationRequiresPaginationEvidence(t *testing.T) {
	definition := discoveryDefinition(t)
	compiled := compileTestDefinition(t, definition)
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	definition.ModelSamples = definition.ModelSamples[:1]
	missing := compileTestDefinition(t, definition)
	if report := Verify(t.Context(), missing); report.Passed {
		t.Fatal("pagination enabled without continuation evidence")
	}
	if compiled.SamplesHash() == missing.SamplesHash() {
		t.Fatal("discovery evidence excluded from samples hash")
	}
	operations := compiled.Operations()
	operations["models"].Models.Decode.Transform.Path = "/changed"
	page, err := compiled.DecodeModelPage(t.Context(), "models", fixtureValue(t, ModelPage{Models: []DiscoveredModel{{ID: "m"}}}))
	if err != nil || len(page.Models) != 1 {
		t.Fatal("operation copy mutated compiled mapping", page, err)
	}
}

func TestDiscoveryRejectsMalformedCatalogValues(t *testing.T) {
	compiled := compileTestDefinition(t, discoveryDefinition(t))
	for _, raw := range []string{`{}`, `{"models":null}`, `{"models":[{"id":null}]}`, `{"models":[{"id":""}]}`, `{"models":[{"id":"a"},{"id":"a"}]}`, `{"models":[{"id":"a","maxTokens":1.5}]}`, `{"models":[{"id":"a","maxTokens":-1}]}`, `{"models":[],"next":null}`, `{"models":[],"url":"https://other.invalid"}`} {
		t.Run(raw, func(t *testing.T) {
			value, _ := ParseValue([]byte(raw))
			if _, err := compiled.DecodeModelPage(t.Context(), "models", value); err == nil {
				t.Fatal("malformed catalog accepted")
			}
		})
	}
}

func TestDiscoveryCompilerRejectsCredentialAndMappingConflicts(t *testing.T) {
	for _, change := range []func(*Operation){
		func(operation *Operation) { operation.Method = "POST" },
		func(operation *Operation) { operation.Models = nil },
		func(operation *Operation) { operation.Models.CursorParameter = "api_key" },
		func(operation *Operation) { operation.Query = map[string]string{"cursor": "static"} },
		func(operation *Operation) { operation.Auth = Credential{Location: "query", Name: "cursor"} },
		func(operation *Operation) { operation.Models.Decode.Capabilities = CapabilitySet{TextCapability: true} },
	} {
		definition := discoveryDefinition(t)
		operation := definition.Operations["models"]
		change(&operation)
		definition.Operations["models"] = operation
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		raw, _ := json.Marshal(definition)
		if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
			t.Fatal("invalid discovery operation compiled")
		}
	}
}
