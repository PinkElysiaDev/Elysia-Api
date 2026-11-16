package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func bindFixtureOutput(t testing.TB, compiled *p.Compiled, options p.EvaluationContext) (p.EvaluationContext, func([]p.Value)) {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(options.Scope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validation.Release)
	consume := func(frames []p.Value) {
		t.Helper()
		for _, frame := range frames {
			if err := validation.Consume(t.Context(), frame); err != nil {
				t.Fatal("target Spec", frame.JSONString(), err)
			}
		}
		if len(frames) != 0 {
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
	}
	return options.WithOutputStream(validation), consume
}

// convertFixtureStream exercises the same declared boundaries as the host:
// source Spec, source Mapping, target projection, target Mapping and target Spec.
func convertFixtureStream(t testing.TB, from, to *p.Compiled, bodies []string) []p.Value {
	t.Helper()
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	if err != nil {
		t.Fatal(err)
	}
	sourceOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer sourceOptions.State.Release()
	targetOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer targetOptions.State.Release()
	includeUsage := true
	targetOptions.ClientOutput = &p.ClientOutput{IncludeUsage: &includeUsage}
	route := fixtureConversionContext(t, conversion, p.ConversionEvent, from.Identity())
	route.Transport = p.SSE
	eventState := p.NewConversionEventState(conversion, route)
	defer eventState.Release()
	sourceValidation, err := from.NewWireInputStreamValidation(sourceOptions.Scope)
	if err != nil {
		t.Fatal(err)
	}
	targetValidation, err := to.NewWireStreamValidation(targetOptions.Scope)
	if err != nil {
		t.Fatal(err)
	}
	targetOptions = targetOptions.WithOutputStream(targetValidation).WithConversion(conversion, route)
	replay, err := p.NewEventReplay(to.Target(p.EncodeEvent, targetOptions.Scope), to.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	var output []p.Value
	consume := func(frames []p.Value) {
		t.Helper()
		for _, frame := range frames {
			if err := targetValidation.Consume(t.Context(), frame); err != nil {
				t.Fatal("target Spec", frame.JSONString(), err)
			}
			output = append(output, frame)
			if err := targetValidation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
	}
	project := func(events []p.Event) []p.Event {
		t.Helper()
		var projected []p.Event
		for _, event := range events {
			event, err = conversion.Event(t.Context(), event, route, nil)
			if err != nil {
				t.Fatal("target projection", event.Type, err)
			}
			accepted, err := replay.Consume(event)
			if err != nil {
				t.Fatal("core lifecycle", err)
			}
			if accepted {
				projected = append(projected, event)
			}
		}
		return projected
	}
	for index, body := range bodies {
		frame, err := sourceValidation.DecodeFrame(t.Context(), testValue(t, body), sourceOptions)
		if err != nil {
			t.Fatal("source Spec/Mapping", index, err)
		}
		route, err = conversion.WithFrameUsage(route, frame.Events)
		if err != nil {
			t.Fatal(err)
		}
		var projected []p.Event
		for _, event := range frame.Events {
			queued, err := eventState.PushContext(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			projected = append(projected, project(queued)...)
		}
		if len(frame.Events) > 0 && len(projected) == 0 {
			continue
		}
		if eventState.Buffered {
			frame = &p.EventFrame{Events: projected}
		}
		frames, err := to.EncodeConvertedFrame(t.Context(), frame, projected, targetOptions)
		if err != nil {
			t.Fatal("target Mapping", index, err)
		}
		consume(frames)
	}
	if err := sourceValidation.Finish(); err != nil {
		t.Fatal("source completion", err)
	}
	tail, err := eventState.Drain()
	if err != nil {
		t.Fatal(err)
	}
	route, err = conversion.WithFrameUsage(route, tail)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range project(tail) {
		frames, err := to.EncodeFrames(t.Context(), event, targetOptions)
		if err != nil {
			t.Fatal(err)
		}
		consume(frames)
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	frames, err := to.FinishEvents(t.Context(), targetOptions)
	if err != nil {
		t.Fatal(err)
	}
	consume(frames)
	if err := targetValidation.Finish(); err != nil {
		t.Fatal("target completion", err)
	}
	return output
}
