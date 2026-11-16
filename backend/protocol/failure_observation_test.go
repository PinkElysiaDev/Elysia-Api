package protocol

import (
	"errors"
	"testing"
)

func failureObservationPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	mapping := document.Mapping.Directions[DecodeEvent]
	condition := Expression{Op: "present", Source: expressionPointer(readExpression("input", "/failure"))}
	observation := objectExpression(map[string]Expression{"error": readExpression("input", "/failure"), "status": literalExpression("failed")})
	mapping.FailureObservation = &Expression{Op: "if", When: &condition, Then: &observation}
	document.Mapping.Directions[DecodeEvent] = mapping
	return document
}

func TestPackageFailureObservationRetainsRejectedSourceEvidence(t *testing.T) {
	compiled := compilePackageForTest(t, failureObservationPackage(t))
	frame := parseTestValue(t, `{"kind":"unregistered","response_id":"r","seq":0,"failure":{"category":"rate_limit","message":"capacity","code":"busy"}}`)
	for _, useValidator := range []bool{false, true} {
		options := EvaluationContext{State: NewEvaluationState()}
		defer options.State.Release()
		decode := compiled.DecodeFrame
		if useValidator {
			validation, err := compiled.NewWireInputStreamValidation(Scope{})
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			decode = validation.DecodeFrame
		}
		events, err := decode(t.Context(), frame, options)
		var failure *GenerationFailure
		var contract *ConversionError
		if events != nil || !errors.As(err, &failure) || !errors.As(err, &contract) || contract.Issues[0].Path != "/kind" {
			t.Fatal("rejected frame lost its contract or failure", events, err)
		}
		fields, _ := failure.Payload.ReadObject()
		if !fields["message"].IsString("capacity") || !fields["category"].IsString("rate_limit") {
			t.Fatal("failure evidence changed", failure.Payload)
		}
		if next, err := decode(t.Context(), packageStreamFrame(ResponseStarted, 1, ""), options); err == nil || next != nil {
			t.Fatal("rejected source resumed", next, err)
		}
	}
	without := failureObservationPackage(t)
	mapping := without.Mapping.Directions[DecodeEvent]
	mapping.FailureObservation = nil
	without.Mapping.Directions[DecodeEvent] = mapping
	plain := compilePackageForTest(t, without)
	if plain.mappings[DecodeEvent].definitionHash == compiled.mappings[DecodeEvent].definitionHash {
		t.Fatal("observation did not affect the source fingerprint")
	}
	_, err := plain.DecodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()})
	var failure *GenerationFailure
	if errors.As(err, &failure) {
		t.Fatal("undeclared failure semantics inferred", err)
	}
}

func TestPackageFailureObservationCannotReadHostOrStreamState(t *testing.T) {
	for _, from := range []string{"runtime", "context", "state"} {
		document := failureObservationPackage(t)
		mapping := document.Mapping.Directions[DecodeEvent]
		mapping.FailureObservation = expressionPointer(readExpression(from, "/failure"))
		document.Mapping.Directions[DecodeEvent] = mapping
		compiler, _ := NewCompiler(DefaultLimits(), nil)
		value, _ := EncodeValue(document)
		if compiled, issues := compiler.CompilePackage(value.Bytes()); compiled != nil || IssuesError(issues) == nil {
			t.Fatal("observation gained nonlocal read authority", from)
		}
	}
}
