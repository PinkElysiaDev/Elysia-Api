package protocol

import (
	"context"
	"errors"
	"testing"
)

func TestWireContractSurvivesMappingReplacement(t *testing.T) {
	document := textProtocolPackage(t)
	contract := document.Spec.Documents["client-response"]
	contract.Assertions = []WireAssertion{{Path: "/reply", Schema: &ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: "reply must remain text"}}
	document.Spec.Documents["client-response"] = contract
	compiled := compilePackageForTest(t, document)
	compiled.mappings[EncodeResponse] = compiledMapping{}
	value := fixtureValue(t, map[string]any{"reply": 42})
	if err := compiled.ValidateWireOutput(EncodeResponse, value); err == nil {
		t.Fatal("replacing execution removed the independent wire contract")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compiled.ValidateWireOutputContext(ctx, EncodeResponse, value); !errors.Is(err, context.Canceled) {
		t.Fatal("wire contract lost cancellation", err)
	}
}

func TestWireContractStreamStateIsRequestOwned(t *testing.T) {
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	first, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	defer second.Release()
	start := parseTestValue(t, `{"kind":"response.started","response_id":"r","seq":0}`)
	finish := parseTestValue(t, `{"kind":"response.finished","response_id":"r","seq":1}`)
	if err := first.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if err := first.Consume(t.Context(), start); err == nil {
		t.Fatal("repeated wire start accepted")
	}
	if err := second.Consume(t.Context(), start); err != nil {
		t.Fatal("wire lifecycle leaked between streams", err)
	}
	if err := second.Consume(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	if err := second.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := first.Finish(); err == nil {
		t.Fatal("finishing another stream repaired a rejected lifecycle")
	}
}
