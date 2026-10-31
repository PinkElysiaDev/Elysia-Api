package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestInputViewReaderOwnership(t *testing.T) {
	input := parseTestValue(t, `{"id":"small","payload":"`+strings.Repeat("x", requestViewMinBytes)+`"}`)
	view := newInputView(input)
	if view.owned.index == nil || view.owned.index != view.borrowed.index {
		t.Fatal("input readers did not share the structural index")
	}
	pointer := []string{"id"}
	borrowed, err := readValuePointerWithObjectReader(input, pointer, &view.borrowed, view.objectReader())
	if err != nil {
		t.Fatal(err)
	}
	owned, err := readValuePointerWithObjectReader(input, pointer, &view.owned, view.objectReader())
	if err != nil || owned.raw != borrowed.raw {
		t.Fatal("read modes changed the selected value", err)
	}
	if spanIdentity(owned.raw) == spanIdentity(borrowed.raw) {
		t.Fatal("mapping retained a small borrowed field")
	}
}

func TestInputViewValidationReceipts(t *testing.T) {
	limits := DefaultLimits()
	contract, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}}, "/spec/document", limits)
	if err != nil {
		t.Fatal(err)
	}
	validator := contract.validator.(*specWireValidator)
	view := newInputView(parseTestValue(t, `{"id":"r"}`))
	if err := validator.validateInputView(t.Context(), DecodeEvent, view, limits); err != nil {
		t.Fatal(err)
	}
	if err := view.checkResources(t.Context(), view.value, limits, "mapping_input"); err != nil {
		t.Fatal(err)
	}
	strict := limits
	strict.BufferBytes = 1
	var budget *ResourceLimitError
	if err := view.checkResources(t.Context(), view.value, strict, "mapping_input"); !errors.As(err, &budget) || budget.Domain != "mapping_input" {
		t.Fatal("root receipt bypassed a different input budget", err)
	}
	if err := validator.validateInputView(t.Context(), EncodeEvent, view, strict); !errors.As(err, &budget) {
		t.Fatal("spec receipt bypassed a different direction or budget", err)
	}
	other, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: ObjectType, Required: []string{"required"}, Properties: map[string]ValueSchema{"required": {Type: StringType}}, AllowUnknown: true}}, "/spec/other", limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.validator.(*specWireValidator).validateInputView(t.Context(), DecodeEvent, view, limits); err == nil {
		t.Fatal("a different validator reused the successful receipt")
	}
	invalid := newInputView(parseTestValue(t, `[]`))
	for range 2 {
		if err := validator.validateInputView(t.Context(), DecodeEvent, invalid, limits); err == nil || invalid.validator != nil {
			t.Fatal("failed spec validation created a receipt", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validator.validateInputView(ctx, DecodeEvent, view, limits); !errors.Is(err, context.Canceled) {
		t.Fatal("successful validation hid cancellation", err)
	}
}

func TestWireSourceDecodeFrameSharesReadsWithoutSharingState(t *testing.T) {
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	scope := Scope{Provider: "local", Account: "synthetic"}
	combined, err := compiled.NewWireInputStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	separate, err := compiled.NewWireInputStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{Scope: scope, State: NewEvaluationState()}
	baselineOptions := EvaluationContext{Scope: scope, State: NewEvaluationState()}
	start := packageStreamFrame(ResponseStarted, 0, "")
	if _, err := combined.DecodeFrame(t.Context(), start, EvaluationContext{}); err == nil {
		t.Fatal("source decode accepted an unrelated scope")
	}
	for _, frame := range []Value{start, packageStreamFrame(ResponseFinished, 1, "")} {
		got, err := combined.DecodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal(err)
		}
		if err := separate.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		want, err := compiled.DecodeFrame(t.Context(), frame, baselineOptions)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("shared reads changed the decoded frame", err, got, want)
		}
	}
	if err := errors.Join(combined.Finish(), separate.Finish(), compiled.FinishInputEvents(t.Context(), baselineOptions)); err != nil {
		t.Fatal("source decoding did not complete the independent lifecycle", err)
	}
	output, err := compiled.NewWireStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.DecodeFrame(t.Context(), start, options); err == nil {
		t.Fatal("target validation was used as a source decoder")
	}
}

func TestWireSourceDecodeFrameCannotHideInvalidWire(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.Directions[DecodeEvent].Program.Fields["sequence"] = literalExpression(99)
	compiled := compilePackageForTest(t, document)
	validation, err := compiled.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	if _, err := validation.DecodeFrame(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), options); err != nil {
		t.Fatal(err)
	}
	repeated := packageStreamFrame(ResponseFinished, 0, "")
	if _, err := compiled.DecodeEvents(t.Context(), repeated, options); err != nil {
		t.Fatal("fixture mapping did not hide the raw sequence", err)
	}
	_, err = validation.DecodeFrame(t.Context(), repeated, options)
	assertPackageRuntimeIssue(t, err, UpstreamContractViolation, "/seq")
}
