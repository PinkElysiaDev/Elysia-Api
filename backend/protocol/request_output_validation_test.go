package protocol

import (
	"context"
	"strings"
	"testing"
)

func TestPackageFinalRequestIntentCheck(t *testing.T) {
	document := textProtocolPackage(t)
	requestSchema := document.Spec.Documents["generation-request"].Schema
	requestSchema.Properties["store"] = ValueSchema{Type: BooleanType}
	check := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("runtime", "/clientOutput/responsesStorage"))}),
		Then:      expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/store"), readExpression("runtime", "/clientOutput/responsesStorage/effective")}}),
		Otherwise: expressionPointer(literalExpression(true))}
	entry := document.Mapping.Directions[EncodeRequest]
	document.Mapping.Expressions = map[string]Expression{"caller-intent": check}
	entry.FinalCheck = expressionPointer(Expression{Op: "ref", Ref: "caller-intent"})
	document.Mapping.Directions[EncodeRequest] = entry
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{ClientOutput: &ClientOutput{ResponsesStorage: &ResponsesStorageIntent{}}}
	for _, tc := range []struct {
		wire    string
		isValid bool
	}{
		{`{"model":"m","prompt":"p","store":false}`, true},
		{`{"model":"m","prompt":"p","store":true}`, false},
		{`{"model":"m","prompt":"p","store":"false"}`, false},
	} {
		wire, err := ParseValue([]byte(tc.wire))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.ValidateRequestOutput(t.Context(), wire, options); (err == nil) != tc.isValid {
			t.Fatal("final wire and prepared intent diverged", tc.wire, err)
		}
		if wire.JSONString() != tc.wire || options.ClientOutput.ResponsesStorage.Effective {
			t.Fatal("final check changed wire or target decision")
		}
	}
	if err := compiled.ValidateRequestOutput(t.Context(), fixtureValue(t, map[string]any{"model": "m", "prompt": "p"}), EvaluationContext{}); err != nil {
		t.Fatal("a bare codec call did not follow the declared no-owner branch", err)
	}
	document.Mapping.Expressions["caller-intent"] = literalExpression(true)
	changed := compilePackageForTest(t, document)
	if compiled.mappings[EncodeRequest].definitionHash == changed.mappings[EncodeRequest].definitionHash {
		t.Fatal("final request named program was omitted from the mapping hash")
	}
}

func TestPackageFinalRequestCheckRunsAfterWireRules(t *testing.T) {
	document := textProtocolPackage(t)
	document.Spec.Documents["generation-request"].Schema.Properties["store"] = ValueSchema{Type: BooleanType}
	decoder := document.Mapping.Directions[DecodeRequest]
	decoder.Program.Fields["clientOutput"] = objectExpression(map[string]Expression{"responsesStorage": literalExpression(ResponsesStorageIntent{})})
	document.Mapping.Directions[DecodeRequest] = decoder
	encoder := document.Mapping.Directions[EncodeRequest]
	encoder.Program.Fields["store"] = literalExpression(false)
	encoder.FinalCheck = expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/store"), readExpression("runtime", "/clientOutput/responsesStorage/effective")}})
	document.Mapping.Directions[EncodeRequest] = encoder
	compiled := compilePackageForTest(t, document)
	wireEdit := Expression{Op: "merge", Policy: "last", Items: []Expression{readExpression("input", ""), objectExpression(map[string]Expression{"store": literalExpression(true)})}}
	conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "change-wire", Rules: []ConversionRule{{ID: "store", Order: 1, Enabled: true, Phase: ConversionWire, Action: "transform", Expression: &wireEdit}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), conversionVerificationKey{}, conversion)
	sample := Sample{ID: "intent", Direction: DecodeRequest, Input: fixtureValue(t, map[string]any{"model": "m", "prompt": "p"})}
	if err := verifyRequestCombination(ctx, compiled, compiled, sample); err == nil || !strings.Contains(err.Error(), "final request") {
		t.Fatal("combination evidence ignored the final wire mutation", err)
	}
	// Both the editor and independent fixtures use the same final boundary.
	encoder.Program.Fields["store"] = literalExpression(true)
	document.Mapping.Directions[EncodeRequest] = encoder
	compiled = compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	input := fixtureValue(t, request)
	if _, err := executeVerificationSample(t.Context(), compiled, Sample{ID: "intent", Direction: EncodeRequest, Input: input}); err == nil {
		t.Fatal("independent verification omitted the final request check")
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	service := &Service{compiler: compiler}
	if result := service.Preview(t.Context(), fixtureValue(t, document).Bytes(), EncodeRequest, input, false, EvaluationContext{}); IssuesError(result.Issues) == nil {
		t.Fatal("editor preview omitted the final request check")
	}
}

func TestPackageFinalRequestCheckPermissions(t *testing.T) {
	for _, read := range []Expression{
		readExpression("runtime", "/operation/id"), readExpression("state", "/slot"),
	} {
		document := textProtocolPackage(t)
		entry := document.Mapping.Directions[EncodeRequest]
		entry.FinalCheck = expressionPointer(Expression{Op: "exists", Source: &read})
		document.Mapping.Directions[EncodeRequest] = entry
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		_, issues := compiler.Compile(fixtureValue(t, document).Bytes())
		if err := IssuesError(issues); err == nil || !strings.Contains(err.Error(), "finalCheck") {
			t.Fatal("final request predicate acquired undeclared runtime or state access", issues)
		}
	}
	document := textProtocolPackage(t)
	entry := document.Mapping.Directions[DecodeRequest]
	entry.FinalCheck = expressionPointer(literalExpression(true))
	document.Mapping.Directions[DecodeRequest] = entry
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	_, issues := compiler.Compile(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err == nil || !strings.Contains(err.Error(), "requires request encoding") {
		t.Fatal("a non-request-output direction declared an unused final check", issues)
	}
}
