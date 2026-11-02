package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func decodePackageStream(t *testing.T, compiled *p.Compiled, raws []string, scope p.Scope) ([]*p.EventFrame, *p.Response) {
	t.Helper()
	options := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	target := compiled.Target(p.DecodeEvent, scope)
	target.Direction = p.EncodeEvent
	collector, err := p.NewResponseCollector(target, compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	var frames []*p.EventFrame
	for index, raw := range raws {
		value := testValue(t, raw)
		frame, err := validation.DecodeFrame(t.Context(), value, options)
		if err != nil {
			t.Fatal("validated source mapping", index, err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal("source semantic lifecycle", index, err)
			}
		}
		frames = append(frames, frame)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return frames, response
}

func encodePackageStream(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, scope p.Scope, preserve bool) []string {
	t.Helper()
	return encodePackageStreamWithOptions(t, compiled, frames, p.EvaluationContext{Scope: scope}, preserve)
}

func encodePackageStreamWithOptions(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, options p.EvaluationContext, preserve bool) []string {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(options.Scope)
	if err != nil {
		t.Fatal(err)
	}
	options.State = p.NewEvaluationState()
	options = options.WithOutputStream(validation)
	options, err = options.WithOperation("fixture-execution", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer options.State.Release()
	var result []string
	consume := func(values []p.Value) {
		for _, value := range values {
			if err := validation.Consume(t.Context(), value); err != nil {
				t.Fatal("target contract", value.JSONString(), err)
			}
			result = append(result, value.JSONString())
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index, frame := range frames {
		if !preserve {
			frame = &p.EventFrame{Events: frame.Events}
		}
		values, err := compiled.EncodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal("target mapping", index, err)
		}
		consume(values)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal("target finalize", err)
	}
	consume(tail)
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	return result
}
