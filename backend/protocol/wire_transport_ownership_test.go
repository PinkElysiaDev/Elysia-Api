package protocol

import (
	"strings"
	"testing"
)

// A valid Mapping may return a large leaf from an even larger semantic value.
// Immutable Value ownership does not imply a standalone backing allocation.
func TestEncodedRequestValueMayShareLargeParameterStorage(t *testing.T) {
	document := textProtocolPackage(t)
	mapping := document.Mapping.Directions[EncodeRequest]
	mapping.Program = readExpression("input", "/parameters/container/wire")
	mapping.Effects = []ProtocolSemanticEffect{{ID: "wire", Kind: "emit", Path: "/parameters/container/wire", Target: ""}}
	document.Mapping.Directions[EncodeRequest] = mapping
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hi"}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	container := parseTestValue(t, `{"wire":{"model":"m","prompt":"`+strings.Repeat("x", 256<<10)+`"},"unused":"`+strings.Repeat("p", 512<<10)+`"}`)
	request.Parameters = Object{"container": container}
	wire, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !sharesValueStorage(container, wire) || wire.Len() >= container.Len()/2 {
		t.Fatal("fixture must expose a large borrowed wire leaf", wire.Len(), container.Len())
	}
	body := wire.Bytes()
	if string(body) != wire.JSONString() {
		t.Fatal("transport copy changed bytes")
	}
	body[0] = '['
	if wire.JSONString()[0] != '{' {
		t.Fatal("public copy modified immutable wire")
	}
}
