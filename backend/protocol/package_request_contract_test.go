package protocol

import "testing"

func TestPackageRequestFieldsNeedNoProviderRegistry(t *testing.T) {
	document := textProtocolPackage(t)
	for _, name := range []string{"request", "generation-request"} {
		contract := document.Spec.Documents[name]
		contract.Schema.Properties["tracking"] = ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"score": {Type: IntegerType}}}
		document.Spec.Documents[name] = contract
	}
	decode := document.Mapping.Directions[DecodeRequest]
	decode.Program.Fields["parameters"] = objectExpression(map[string]Expression{"metadata": readExpression("input", "/tracking")})
	document.Mapping.Directions[DecodeRequest] = decode
	encode := document.Mapping.Directions[EncodeRequest]
	encode.Program.Fields["tracking"] = readExpression("input", "/parameters/metadata")
	document.Mapping.Directions[EncodeRequest] = encode
	compiled := compilePackageForTest(t, document)
	wire := []byte(`{"model":"m","prompt":"hello","tracking":{"score":7}}`)
	request, err := compiled.DecodeRequest(t.Context(), wire, EvaluationContext{})
	if err != nil {
		t.Fatal("a default protocol's tracking types leaked into this package", err)
	}
	request.Native = nil
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	request, err = conversion.Request(t.Context(), request, ConversionContext{}, nil)
	if err != nil {
		t.Fatal("shared conversion validation imposed an external field type", err)
	}
	output, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
	if err != nil || !equalValues(output, parseTestValue(t, string(wire))) {
		t.Fatal("declared tracking field was not rebuilt", output, err)
	}
	request.Parameters["metadata"] = parseTestValue(t, `{"score":"wrong"}`)
	if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{}); err == nil {
		t.Fatal("edited tracking data bypassed its declared Spec")
	}
}
