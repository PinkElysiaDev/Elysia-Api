package protocol

import "testing"

func TestPackageMetadataContractNeedsNoProviderRegistry(t *testing.T) {
	document := textProtocolPackage(t)
	for _, name := range []string{"provider-response", "client-response"} {
		contract := document.Spec.Documents[name]
		contract.Schema.Properties["trace_label"] = ValueSchema{Type: StringType}
		document.Spec.Documents[name] = contract
	}
	decode := document.Mapping.Directions[DecodeResponse]
	decode.Program.Fields["metadata"] = Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
		"codec": literalExpression(document.Spec.Family), "sourceCodec": literalExpression(document.Spec.Family),
		"location": literalExpression("response"), "name": literalExpression("trace_label"),
		"path": literalExpression("/trace_label"), "value": readExpression("input", "/trace_label"),
	})}}
	document.Mapping.Directions[DecodeResponse] = decode
	encode := document.Mapping.Directions[EncodeResponse]
	encode.Program.Fields["trace_label"] = readExpression("input", "/metadata/0/value")
	document.Mapping.Directions[EncodeResponse] = encode
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "metadata", Order: 220, Enabled: true, Phase: ConversionResponse,
		Steps: []ProtocolControlStep{{Each: &ProtocolControlEach{Select: "metadata", Steps: []ProtocolControlStep{{
			When: expressionPointer(Expression{Op: "not", Source: expressionPointer(Expression{Op: "equal", Items: []Expression{
				Expression{Op: "type", Source: expressionPointer(readExpression("input", "/value"))}, literalExpression("string"),
			}})}), Diagnostic: &ProtocolControlDiagnostic{Kind: "reject", Source: expressionPointer(readExpression("input", "/path")), Reason: literalExpression("trace label requires text")},
		}}}}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	response, err := compiled.DecodeResponse(t.Context(), []byte(`{"reply":"hello","trace_label":"local"}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	response.Native = nil
	response, err = conversion.Response(t.Context(), response, conversion.VerificationRoute(compiled.Identity(), compiled.Identity(), HTTPJSON), nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ParseValue(wire)
	if err != nil || !equalValues(fixtureValue(t, map[string]string{"reply": "hello", "trace_label": "local"}), actual) {
		t.Fatal("metadata was not rebuilt from Mapping", string(wire))
	}
	if _, err := compiled.DecodeResponse(t.Context(), []byte(`{"reply":"hello","trace_label":7}`), EvaluationContext{}); err == nil {
		t.Fatal("Spec accepted the wrong external field type")
	}
	response.Metadata[0].Value = fixtureValue(t, 7)
	if _, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(compiled.Identity(), compiled.Identity(), HTTPJSON), nil); err == nil {
		t.Fatal("Mapping accepted an edited metadata field with the wrong type")
	}
	if _, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{}); err == nil {
		t.Fatal("final Spec accepted an edited external field with the wrong type")
	}
	response.Metadata[0].Value = StringValue("changed")
	response.Metadata[0].Name = "custom_field"
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Metadata: response.Metadata}
	if err := IssuesError(CheckEvent(event, Target{Protocol: compiled.Identity(), coreVersion: ProtocolCoreVersion}, DefaultLimits())); err != nil {
		t.Fatal("core event validation consulted an external metadata registry", err)
	}
}
