package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func discoveryPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := verifiedTextPackage(t, "catalog")
	document.Spec.Documents["catalog-page"] = WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}}
	document.Spec.Operations["models"] = ProtocolOperationSpec{Kind: "models", Method: "GET", Path: "/catalog", Transport: HTTPJSON,
		Response: "catalog-page", Auth: Credential{Location: "none"}, Models: &ProtocolModelDiscoverySpec{CursorParameter: "cursor"}}
	document.Mapping.Operations["models"] = ProtocolOperationMapping{DecodeModels: expressionPointer(Expression{Op: "ref", Ref: "catalog-page"})}
	document.Mapping.Expressions = map[string]Expression{"catalog-page": readExpression("input", "")}
	document.Mapping.ModelSamples = discoveryDefinition(t).ModelSamples
	return document
}

func TestPackageModelsUseDeclaredProgramAndEvidence(t *testing.T) {
	document := discoveryPackage(t)
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(fixtureValue(t, document.Spec).Bytes(), fixtureValue(t, document.Mapping).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	legacy := compileTestDefinition(t, discoveryDefinition(t))
	for _, sample := range document.Mapping.ModelSamples {
		got, err := compiled.DecodeModelPage(t.Context(), "models", sample.Input)
		want, legacyErr := legacy.DecodeModelPage(t.Context(), "models", sample.Input)
		if err != nil || legacyErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("package discovery changed the catalog", got, want, err, legacyErr)
		}
	}
	report := Verify(t.Context(), compiled)
	if !report.Passed || len(report.Checks) != len(document.Mapping.Samples)+len(document.Mapping.ModelSamples) {
		t.Fatal("package model samples were skipped", report)
	}
	document.Mapping.ModelSamples = document.Mapping.ModelSamples[:1]
	missing := compilePackageForTest(t, document)
	if Verify(t.Context(), missing).Passed || missing.SamplesHash() == compiled.SamplesHash() || IssuesError(CanActivate(missing, report)) == nil {
		t.Fatal("changed or missing discovery evidence reused verification")
	}
	samples := compiled.verificationModelSamples()
	samples[0].ID = "caller-edit"
	if compiled.verificationModelSamples()[0].ID == "caller-edit" {
		t.Fatal("model evidence accessor exposed mutable fixtures")
	}
	operation, _ := compiled.Operation("models")
	if operation.Request != "" || operation.Response != "" || operation.Models.Decode.Transform != nil {
		t.Fatal("package discovery was converted into a legacy executable mapping")
	}
	operation.Models.CursorParameter = "changed"
	if pinned, _ := compiled.Operation("models"); pinned.Models.CursorParameter != "cursor" {
		t.Fatal("operation copy changed pinned pagination")
	}
}

func TestPackageModelsOnlyDoesNotClaimGeneration(t *testing.T) {
	document := discoveryPackage(t)
	delete(document.Spec.Operations, "generate")
	delete(document.Mapping.Operations, "generate")
	document.Mapping.Directions = nil
	document.Mapping.Samples = nil
	document.Mapping.Capabilities = nil
	compiled := compilePackageForTest(t, document)
	report := Verify(t.Context(), compiled)
	if !report.Passed || len(report.Covered) != 0 || compiled.Supports(DecodeRequest) || compiled.Supports(DecodeResponse) {
		t.Fatal("catalog-only package acquired generation evidence", report)
	}
	document.Mapping.ModelSamples = nil
	if Verify(t.Context(), compilePackageForTest(t, document)).Passed {
		t.Fatal("catalog-only package passed without model evidence")
	}
}

func TestPackageModelsMapProviderCatalog(t *testing.T) {
	document := discoveryPackage(t)
	document.Mapping.Expressions["catalog-page"] = objectExpression(map[string]Expression{
		"models": {Op: "map", Source: expressionPointer(readExpression("input", "/data")), Body: expressionPointer(objectExpression(map[string]Expression{
			"id": readExpression("item", "/model_id"), "name": readExpression("item", "/label"), "maxTokens": readExpression("item", "/limit"),
		}))},
		"next": readExpression("input", "/cursor"),
	})
	compiled := compilePackageForTest(t, document)
	input := parseTestValue(t, `{"data":[{"model_id":"m","label":"Model","limit":1000000}],"cursor":"next"}`)
	page, err := compiled.DecodeModelPage(t.Context(), "models", input)
	if err != nil || !reflect.DeepEqual(page, ModelPage{Models: []DiscoveredModel{{ID: "m", Name: "Model", MaxTokens: 1000000}}, Next: "next"}) {
		t.Fatal("declared catalog transform was not executed", page, err)
	}
	document.Spec.Operations["models"].Models.CursorParameter = ""
	unpaged := compilePackageForTest(t, document)
	if _, err := unpaged.DecodeModelPage(t.Context(), "models", input); err == nil {
		t.Fatal("undeclared pagination was accepted")
	}
}

func TestPackageModelsValidateWireBeforeProgram(t *testing.T) {
	document := discoveryPackage(t)
	delete(document.Mapping.Expressions, "catalog-page")
	document.Mapping.Operations["models"] = ProtocolOperationMapping{DecodeModels: expressionPointer(literalExpression(ModelPage{Models: []DiscoveredModel{}}))}
	wire := document.Spec.Documents["catalog-page"]
	wire.Assertions = []WireAssertion{{Path: "/valid", Schema: &ValueSchema{Type: BooleanType, Enum: []Value{Value{raw: "true"}}}, Code: UpstreamContractViolation, Reason: "invalid catalog wire"}}
	document.Spec.Documents["catalog-page"] = wire
	compiled := compilePackageForTest(t, document)
	_, err := compiled.DecodeModelPage(t.Context(), "models", parseTestValue(t, `{"valid":false}`))
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Code != UpstreamContractViolation || failure.Issues[0].Path != "/valid" {
		t.Fatal("constant mapping hid invalid source wire", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.DecodeModelPage(ctx, "models", parseTestValue(t, `{"valid":true}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("catalog mapping ignored cancellation", err)
	}
	oversized := Value{raw: `{"valid":true,"large":"` + strings.Repeat("x", compiled.limits.BufferBytes) + `"}`}
	if _, err := compiled.DecodeModelPage(t.Context(), "models", oversized); !hasIssueCode(err, LimitExceeded) {
		t.Fatal("catalog mapping bypassed the whole-input budget", err)
	}
}

func TestPackageModelsRejectInvalidBindings(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"post", func(p *ProtocolPackage) {
			op := p.Spec.Operations["models"]
			op.Method = "POST"
			p.Spec.Operations["models"] = op
		}},
		{"no-model-config", func(p *ProtocolPackage) {
			op := p.Spec.Operations["models"]
			op.Models = nil
			p.Spec.Operations["models"] = op
		}},
		{"credential-cursor", func(p *ProtocolPackage) { p.Spec.Operations["models"].Models.CursorParameter = "api_key" }},
		{"static-cursor", func(p *ProtocolPackage) {
			op := p.Spec.Operations["models"]
			op.Query = map[string]string{"cursor": "static"}
			p.Spec.Operations["models"] = op
		}},
		{"auth-cursor", func(p *ProtocolPackage) {
			op := p.Spec.Operations["models"]
			op.Auth = Credential{Location: "query", Name: "cursor"}
			p.Spec.Operations["models"] = op
		}},
		{"no-response-document", func(p *ProtocolPackage) {
			op := p.Spec.Operations["models"]
			op.Response = ""
			p.Spec.Operations["models"] = op
		}},
		{"no-program", func(p *ProtocolPackage) { p.Mapping.Operations["models"] = ProtocolOperationMapping{} }},
		{"generation-binding", func(p *ProtocolPackage) {
			op := p.Mapping.Operations["models"]
			op.Response = DecodeResponse
			p.Mapping.Operations["models"] = op
		}},
		{"generation-program", func(p *ProtocolPackage) {
			op := p.Mapping.Operations["generate"]
			op.DecodeModels = expressionPointer(readExpression("input", ""))
			p.Mapping.Operations["generate"] = op
		}},
		{"fake-sample-operation", func(p *ProtocolPackage) { p.Mapping.ModelSamples[0].Operation = "generate" }},
		{"module", func(p *ProtocolPackage) {
			p.Mapping.Operations["models"] = ProtocolOperationMapping{DecodeModels: expressionPointer(Expression{Op: "anthropic"})}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := discoveryPackage(t)
			fixture.change(&document)
			if err := document.Validate(DefaultLimits()); err == nil {
				t.Fatal("invalid model declaration compiled")
			}
		})
	}
}
