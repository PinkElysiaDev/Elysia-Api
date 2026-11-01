package protocol

import (
	"bytes"
	"testing"
)

func TestPackageNativeResponseKeepsCurrentDeliveryIntent(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	for _, name := range []string{"provider-response", "client-response"} {
		schema := document.Spec.Documents[name].Schema
		schema.Properties["store"] = ValueSchema{Type: BooleanType}
		schema.AllowUnknown = true
	}
	decoder := document.Mapping.Directions[DecodeResponse]
	decoder.Program.Fields["attributes"] = objectExpression(map[string]Expression{"store": readExpression("input", "/store")})
	document.Mapping.Directions[DecodeResponse] = decoder
	encoder := document.Mapping.Directions[EncodeResponse]
	encoder.Program.Fields["store"] = Expression{Op: "if",
		When:      expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("runtime", "/clientOutput/responsesStorage"))}),
		Then:      expressionPointer(readExpression("runtime", "/clientOutput/responsesStorage/effective")),
		Otherwise: expressionPointer(readExpression("input", "/attributes/store")),
	}
	document.Mapping.Directions[EncodeResponse] = encoder
	compiled := compilePackageForTest(t, document)
	original := []byte(` { "reply":"hi","store":true,"vendor":{"exact":9007199254740993,"scale":1e+3,"empty":null} } `)
	response, err := compiled.DecodeResponse(t.Context(), original, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatal("unmodified package response lost its native bytes", string(unchanged), err)
	}
	options := EvaluationContext{ClientOutput: &ClientOutput{ResponsesStorage: &ResponsesStorageIntent{Effective: false}}}
	changed, err := compiled.EncodeResponse(t.Context(), response, options)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ParseValue(changed)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := wire.ReadObject()
	if err != nil || fields["store"] != fixtureValue(t, false) || !fields["reply"].IsString("hi") || fields["vendor"].JSONString() != `{"exact":9007199254740993,"scale":1e+3,"empty":null}` {
		t.Fatal("delivery intent or untouched native values changed", string(changed), err)
	}
	if !bytes.Equal(response.Native.Value.Bytes(), original) || response.Attributes["store"] != fixtureValue(t, true) {
		t.Fatal("delivery reconciliation mutated the source response")
	}
}
