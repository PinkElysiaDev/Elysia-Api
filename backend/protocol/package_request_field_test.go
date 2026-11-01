package protocol

import "testing"

func TestRequestFieldDispositionUsesDeclaredValueAndOwner(t *testing.T) {
	document := textProtocolPackage(t)
	encode := document.Mapping.Directions[EncodeRequest]
	encode.Effects = append(encode.Effects, ProtocolSemanticEffect{ID: "mode-default", Kind: "default", Scope: requestFieldScope,
		Path: "/parameters/context_mode", Default: StringValue("complete"), When: expressionPointer(literalExpression(true)), Reason: "target retains supplied history by default"})
	document.Mapping.Directions[EncodeRequest] = encode
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "projection", Order: 130, Enabled: true, Phase: ConversionRequest, Use: "request_projection"}}
	compiled := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Parameters: Object{"context_mode": StringValue("complete")},
		Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hi")}}}}}
	for _, mode := range []string{"compatible", "strict"} {
		policy.Mode = mode
		conversion, err = ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Request(t.Context(), request, route, nil)
		if err != nil || !projected.Parameters["context_mode"].IsZero() || !request.Parameters["context_mode"].IsString("complete") {
			t.Fatal("declared default changed the caller or was not normalized", projected, err)
		}
		if _, err := compiled.EncodeRequestValue(t.Context(), projected, EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
	}
	request.Parameters["context_mode"] = StringValue("future")
	_, err = conversion.Request(t.Context(), request, route, nil)
	assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/parameters/context_mode")
	request.Parameters["context_mode"] = StringValue("complete")
	policy.Rules[0].Enabled = false
	disabled, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := disabled.Request(t.Context(), request, route, nil)
	if err != nil || !retained.Parameters["context_mode"].IsString("complete") {
		t.Fatal("disabled projection still removed its field", retained, err)
	}
	if _, err := compiled.EncodeRequestValue(t.Context(), retained, EvaluationContext{}); err == nil {
		t.Fatal("encoder repeated a disabled field disposition")
	}
	encode.Effects[len(encode.Effects)-1].Kind = "reject"
	encode.Effects[len(encode.Effects)-1].Default = Value{}
	document.Mapping.Directions[EncodeRequest] = encode
	compiled = compilePackageForTest(t, document)
	conversion, err = ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conversion.Request(t.Context(), request, route, nil)
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/parameters/context_mode")
	for _, path := range []string{"/resources", "/native/value", "/clientOutput/rawStreamOptions", "/reasoning", "/parameters/context_mode/child", "/reasoning/mode"} {
		if _, err := compileRequestFieldClear(path); err == nil {
			t.Fatal("field disposition can clear a container or protected owner", path)
		}
	}
}
