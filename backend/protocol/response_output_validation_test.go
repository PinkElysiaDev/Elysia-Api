package protocol

import (
	"context"
	"strings"
	"testing"
)

func deliveryRangeTestPackage(t *testing.T) ProtocolPackage {
	document := textProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeResponse]
	entry.DeliveryRanges = []ProtocolDeliveryRange{{Name: "custom-reply",
		Measure: Expression{Op: "utf16_length", Source: expressionPointer(readExpression("input", ""))},
		Texts: Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
			"key": literalExpression("reply"), "text": readExpression("input", "/reply"), "mode": literalExpression("snapshot"), "path": literalExpression("/reply"),
		})}},
		Ranges: literalExpression([]any{map[string]any{"key": "reply", "id": "citation", "start": 0, "end": 1, "path": "/reply"}}),
	}}
	document.Mapping.Directions[EncodeResponse] = entry
	return document
}

func TestCustomResponseDeliveryRangeBoundary(t *testing.T) {
	document := deliveryRangeTestPackage(t)
	compiled := compilePackageForTest(t, document)
	before := parseTestValue(t, `{"reply":"answer"}`)
	after := parseTestValue(t, `{"reply":"another"}`)
	if err := compiled.ValidateResponseOutput(t.Context(), before, after, EvaluationContext{}); err != nil {
		t.Fatal("the unchanged prefix was rejected", err)
	}
	after = parseTestValue(t, `{"reply":"rewritten"}`)
	if err := compiled.ValidateResponseOutput(t.Context(), before, after, EvaluationContext{}); err == nil {
		t.Fatal("custom configured reference accepted changed text")
	}
	// No provider labels are involved. Changing only the declared range moves
	// this same restriction to the different part of the external reply.
	entry := document.Mapping.Directions[EncodeResponse]
	entry.DeliveryRanges[0].Ranges = literalExpression([]any{map[string]any{"key": "reply", "id": "citation", "start": 2, "end": 3, "path": "/reply"}})
	document.Mapping.Directions[EncodeResponse] = entry
	configured := compilePackageForTest(t, document)
	after = parseTestValue(t, `{"reply":"another"}`)
	if err := configured.ValidateResponseOutput(t.Context(), before, after, EvaluationContext{}); err == nil {
		t.Fatal("configured reference did not change the final boundary")
	}
	if compiled.Hash() == configured.Hash() {
		t.Fatal("delivery declaration was absent from package identity")
	}
}

func TestResponseDeliveryRangesCompositionAndPreview(t *testing.T) {
	document := deliveryRangeTestPackage(t)
	compiled := compilePackageForTest(t, document)
	present := true
	policy := ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "rewrite", Rules: []ConversionRule{{ID: "rewrite", Order: 1000, Enabled: true, Phase: ConversionWire, Action: "set", Path: "/reply", Value: StringValue("changed"), Match: ConversionMatch{Path: "/reply", Present: &present}}}}
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), conversionVerificationKey{}, conversion)
	sample := Sample{ID: "rewrite", Direction: DecodeResponse, Input: parseTestValue(t, `{"reply":"answer"}`)}
	if err := verifyResponseCombination(ctx, compiled, compiled, sample); err == nil || !strings.Contains(err.Error(), "unproven text") {
		t.Fatal("composition missed the final proof", err)
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	service := &Service{compiler: compiler}
	service.snapshot.Store(&registrySnapshot{entries: map[string]*Compiled{compiled.Identity().DefinitionID: compiled}})
	result := service.PreviewWorkflow(t.Context(), PreviewInput{Mode: "conversion", Direction: EncodeResponse, ConversionPhase: ConversionWire, ConversionPolicy: &policy,
		ConversionContext: ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: HTTPJSON}, Input: sample.Input})
	if err := IssuesError(result.Issues); err == nil || !strings.Contains(err.Error(), "unproven text") {
		t.Fatal("wire preview missed the final proof", result.Issues)
	}
}

func TestDeliveryRangeSelectorsCannotAcquireState(t *testing.T) {
	for _, expression := range []Expression{readExpression("state", "/cursor"), readExpression("runtime", "/operation/id"), {Op: "track_position", Name: "stream", Source: expressionPointer(literalExpression("item")), Body: expressionPointer(literalExpression(1))}} {
		document := deliveryRangeTestPackage(t)
		entry := document.Mapping.Directions[EncodeResponse]
		entry.DeliveryRanges[0].Texts = expression
		document.Mapping.Directions[EncodeResponse] = entry
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		if _, issues := compiler.Compile(fixtureValue(t, document).Bytes()); IssuesError(issues) == nil {
			t.Fatal("read-only selector acquired execution state", expression)
		}
	}
}
