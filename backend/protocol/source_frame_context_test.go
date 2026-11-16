package protocol

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func contextEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	wire := document.Spec.Documents["provider-event"]
	wire.Assertions = append(wire.Assertions, WireAssertion{
		Reason: "carrier event name must identify a message",
		Check: expressionPointer(Expression{Op: "equal", Items: []Expression{
			readExpression("context", "/eventName"), literalExpression("message"),
		}}),
	})
	for _, kind := range []EventType{ResponseStarted, ResponseFinished} {
		wire.Stream.Events[string(kind)][0].When = expressionPointer(Expression{Op: "equal", Items: []Expression{
			readExpression("context", "/eventId"), readExpression("input", "/response_id"),
		}})
	}
	document.Spec.Documents["provider-event"] = wire
	document.Mapping.Directions[DecodeEvent].Program.Fields["sessionId"] = readExpression("context", "/eventId")
	return document
}

func TestSourceFrameContextSharedBySpecLifecycleAndMapping(t *testing.T) {
	compiled := compilePackageForTest(t, contextEventPackage(t))
	for _, isExplicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "explicit-validator"}[isExplicit], func(t *testing.T) {
			for _, failure := range []string{"", "spec", "lifecycle", "next-frame"} {
				t.Run("context-"+failure, func(t *testing.T) {
					options := EvaluationContext{State: NewEvaluationState(), Values: Object{
						"eventName": StringValue("message"), "eventId": StringValue("r"),
					}}
					defer options.State.Release()
					decode := compiled.DecodeFrame
					finish := func() error { return compiled.FinishInputEvents(t.Context(), options) }
					if isExplicit {
						validator, err := compiled.NewWireInputStreamValidation(options.Scope)
						if err != nil {
							t.Fatal(err)
						}
						decode, finish = validator.DecodeFrame, validator.Finish
					}
					if failure == "spec" {
						options.Values["eventName"] = StringValue("unknown")
					} else if failure == "lifecycle" {
						options.Values["eventId"] = StringValue("other")
					}
					frame, err := decode(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), options)
					if failure == "spec" || failure == "lifecycle" {
						if err == nil {
							t.Fatal("source accepted conflicting carrier metadata")
						}
						return
					}
					if err != nil || len(frame.Events) != 1 || !frame.Events[0].SessionID.IsString("r") {
						t.Fatal("mapping did not share validated carrier metadata", frame, err)
					}
					if failure == "next-frame" {
						options.Values = nil
					}
					_, err = decode(t.Context(), packageStreamFrame(ResponseFinished, 1, ""), options)
					if failure == "next-frame" {
						if err == nil {
							t.Fatal("carrier metadata leaked into the next frame")
						}
					} else if err != nil || finish() != nil {
						t.Fatal("valid carrier-aware stream did not finish", err)
					}
				})
			}
		})
	}
}

func TestSourceFrameContextReceiptOwnership(t *testing.T) {
	compiled := compilePackageForTest(t, contextEventPackage(t))
	options := EvaluationContext{State: NewEvaluationState(), Values: Object{
		"eventName": StringValue("message"), "eventId": StringValue("r"),
	}}
	defer options.State.Release()
	frame := packageStreamFrame(ResponseStarted, 0, "")
	validated, err := compiled.validateSourceFrame(t.Context(), DecodeEvent, frame, options)
	if err != nil {
		t.Fatal(err)
	}
	metadata := validated.inputView.context
	options.Values["eventName"] = StringValue("unknown")
	if validated.inputView.context != metadata {
		t.Fatal("caller mutation changed an immutable validation view")
	}
	if _, err := compiled.validateSourceFrame(t.Context(), DecodeEvent, frame, validated); err == nil {
		t.Fatal("a successful receipt authorized the same body with different metadata")
	}
	validator, err := compiled.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Consume(t.Context(), frame); err == nil {
		t.Fatal("metadata-free validation inherited another invocation's context")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.DecodeFrame(ctx, frame, options); !errors.Is(err, context.Canceled) {
		t.Fatal("frame context hid cancellation", err)
	}
	if !reflect.DeepEqual(options.Values, Object{"eventName": StringValue("unknown"), "eventId": StringValue("r")}) {
		t.Fatal("validation changed caller metadata")
	}
}
