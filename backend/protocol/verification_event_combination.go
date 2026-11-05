package protocol

import (
	"bytes"
	"context"
)

type combinationEventProjection struct {
	conversion *CompiledConversion
	route      ConversionContext
	state      *ConversionEventState
	collector  *ResponseCollector
	evidence   *continuationEvidence
	events     []Event
}

func (projection *combinationEventProjection) capture(ctx context.Context, source, event Event) (Event, error) {
	collector, evidence := projection.collector, projection.evidence
	if collector == nil {
		return event, nil
	}
	if _, _, err := collector.Consume(source); err != nil {
		return Event{}, err
	}
	if node, _, ok, err := collector.CompletedNode(source); err != nil {
		return Event{}, err
	} else if ok {
		if err := evidence.capture(ctx, []Node{node}); err != nil {
			return Event{}, err
		}
	}
	if source.Type == ResponseFinished {
		response, err := collector.Finish()
		if err != nil {
			return Event{}, err
		}
		if err := evidence.capture(ctx, response.Content); err != nil {
			return Event{}, err
		}
	}
	return collector.BindContinuationEvent(event, evidence.binding)
}

func (projection *combinationEventProjection) push(ctx context.Context, frame *EventFrame) (*EventFrame, error) {
	conversion := projection.conversion
	route, err := conversion.WithFrameUsage(projection.route, frame.Events)
	if err != nil {
		return nil, err
	}
	projection.route = route
	original, _ := EncodeValue(frame.Events)
	events := []Event{}
	deliveryEvents := frame.Events
	if conversion.HasAnthropicEnvelope(ConversionEvent, route) {
		deliveryEvents = DeliveryFrameEvents(frame.Events)
	}
	for index, event := range deliveryEvents {
		event, err := projection.capture(ctx, frame.Events[index], event)
		if err != nil {
			return nil, err
		}
		queued, err := projection.state.PushContext(ctx, event)
		if err != nil {
			return nil, err
		}
		for _, next := range queued {
			if conversion != nil {
				value, err := EncodeValue(next)
				if err != nil {
					return nil, err
				}
				proofRoute, err := conversion.PreviewRecovery(ConversionEvent, value, route)
				if err != nil {
					return nil, err
				}
				next, err = conversion.Event(ctx, next, proofRoute, verificationDiagnostics(ctx))
				if err != nil {
					return nil, err
				}
			}
			events = append(events, next)
		}
	}
	projection.events = append(projection.events, events...)
	if len(frame.Events) > 0 && len(events) == 0 {
		return nil, nil
	}
	updated, _ := EncodeValue(events)
	if projection.state.Buffered || !bytes.Equal(original.Bytes(), updated.Bytes()) {
		return &EventFrame{Events: events}, nil
	}
	return frame, nil
}

func (projection *combinationEventProjection) drain(ctx context.Context, emit func(*EventFrame) error) error {
	tail, err := projection.state.Drain()
	if err != nil {
		return err
	}
	route, err := projection.conversion.WithFrameUsage(projection.route, tail)
	if err != nil {
		return err
	}
	projection.route = route
	for _, event := range tail {
		event, err = projection.conversion.Event(ctx, event, route, verificationDiagnostics(ctx))
		if err != nil {
			return err
		}
		projection.events = append(projection.events, event)
		if err := emit(&EventFrame{Events: []Event{event}}); err != nil {
			return err
		}
	}
	return nil
}

func verifyEventCombination(ctx context.Context, ingress, upstream *Compiled, sample Sample) error {
	conversion, _ := ctx.Value(conversionVerificationKey{}).(*CompiledConversion)
	route := ConversionContext{Source: upstream.Identity(), Target: ingress.Identity(), Transport: SSE, Scope: sample.Scope}
	if conversion != nil {
		route = conversion.VerificationRoute(upstream.Identity(), ingress.Identity(), SSE)
		route.Scope = sample.Scope
	}
	var result verificationResult
	var err error
	isPackageStream := ingress.packagePlan != nil && upstream.packagePlan != nil
	if !isPackageStream {
		result, err = executeEventFixture(ctx, upstream, sample)
		if err != nil {
			return err
		}
	}
	options := newVerificationContext(sample.Scope, sample.Context)
	defer options.State.Release()
	// This suite verifies transport usage independently of a particular client's
	// presentation preference.
	yes := true
	options.ClientOutput = &ClientOutput{IncludeUsage: &yes}
	route = route.WithOperation(options)
	evidence, err := newContinuationEvidence(conversion, ingress, ConversionEvent, route)
	if err != nil {
		return err
	}
	var collector *ResponseCollector
	if evidence != nil {
		defer evidence.binding.Release()
		collector, err = NewResponseCollector(upstream.Target(EncodeEvent, sample.Scope), upstream.limits)
		if err != nil {
			return err
		}
		defer collector.Release()
		route = route.WithContinuation(evidence.binding, route.requireContinuation)
		options = options.WithContinuation(route.continuation)
	}
	eventState := NewConversionEventState(conversion, route)
	defer eventState.Release()
	if isPackageStream {
		budget := NewStreamBudget(ingress.limits)
		budget.constrain(upstream.limits)
		if err := budget.Bind(options.State, eventState); err != nil {
			return err
		}
		if collector != nil {
			if err := budget.Bind(collector, evidence.binding); err != nil {
				return err
			}
		}
	}
	projection := combinationEventProjection{conversion: conversion, route: route, state: eventState, collector: collector, evidence: evidence}
	produce := func(emit func(*EventFrame) error) error {
		convert := func(frame *EventFrame) error {
			frame, err := projection.push(ctx, frame)
			if err != nil || frame == nil {
				return err
			}
			return emit(frame)
		}
		if isPackageStream {
			if _, err := executeEventFixtureStream(ctx, upstream, sample, options, convert); err != nil {
				return err
			}
		} else {
			for _, frame := range result.frames {
				if err := convert(frame); err != nil {
					return err
				}
			}
		}
		return projection.drain(ctx, emit)
	}
	decoded, err := verifyCombinationEventStream(ctx, ingress, options, conversion, route, produce)
	if err != nil {
		return err
	}
	if !ingress.Supports(DecodeEvent) {
		return nil
	}
	var comparison error
	if hasSameWire(ingress, upstream) && ingress.native.Preserve && upstream.native.Preserve && !eventState.Buffered {
		comparison = compareRoundTrip(ingress, sample, projection.events, decoded)
	} else {
		comparison = compareEventSequence(ingress, sample, projection.events, decoded)
	}
	if comparison != nil {
		return comparison
	}
	target := ingress.target(EncodeEvent, options)
	replay, err := NewEventReplay(target, ingress.limits)
	if err != nil {
		return err
	}
	defer replay.Release()
	for _, event := range decoded {
		if _, err := replay.Consume(event); err != nil {
			return err
		}
	}
	return replay.Finish()
}

func verifyCombinationEventStream(ctx context.Context, compiled *Compiled, options EvaluationContext, conversion *CompiledConversion, route ConversionContext, produce func(func(*EventFrame) error) error) ([]Event, error) {
	options = options.WithConversion(conversion, route)
	// Client readback proves the result independently; it is not another state
	// holder in the host's forwarding pipeline.
	readback := options
	readback.State = NewEvaluationState()
	defer readback.State.Release()
	var replay *EventReplay
	if options.State.retention.budget != nil {
		var err error
		replay, err = NewEventReplay(compiled.target(EncodeEvent, options), compiled.limits)
		if err != nil {
			return nil, err
		}
		defer replay.Release()
		if err := options.State.retention.budget.Bind(replay); err != nil {
			return nil, err
		}
	}
	encode := func(consume func([]Value) error) error {
		if err := produce(func(frame *EventFrame) error {
			if replay != nil {
				if err := consumeVerificationEvents(replay, frame.Events); err != nil {
					return err
				}
			}
			batch, err := compiled.EncodeFrame(ctx, frame, options)
			if err != nil {
				return err
			}
			return consume(batch)
		}); err != nil {
			return err
		}
		if replay != nil {
			if err := replay.Finish(); err != nil {
				return err
			}
		}
		tail, err := compiled.FinishEvents(ctx, options)
		if err != nil {
			return err
		}
		return consume(tail)
	}
	var pending []Value
	if compiled.packagePlan == nil {
		// Legacy evidence preserves its encode-before-validation error order.
		if err := encode(func(batch []Value) error {
			pending = append(pending, batch...)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	validation, err := compiled.NewWireStreamValidation(options.Scope)
	if err != nil {
		return nil, err
	}
	defer validation.Release()
	if budget := options.State.retention.budget; budget != nil {
		if err := budget.Bind(validation); err != nil {
			return nil, err
		}
	}
	var decoded []Event
	consume := func(batch []Value) error {
		for _, wire := range batch {
			before := wire
			if conversion != nil && conversion.HasPhase(ConversionWire) {
				route.ResponseWire = true
				var err error
				wire, err = conversion.ApplyValue(ctx, ConversionWire, wire, route, verificationDiagnostics(ctx))
				if err != nil {
					return err
				}
			}
			actual := []Value{wire}
			var originals []Value
			if options.continuation != nil {
				prior, carriers, err := compiled.EncodeContinuationTransformedFrames(ctx, before, wire, options)
				if err != nil {
					return err
				}
				if carriers != nil {
					actual = carriers
					originals = prior
				}
			}
			for index, delivered := range actual {
				var err error
				if compiled.packagePlan != nil {
					original := before
					if originals != nil {
						original = originals[index]
					}
					err = validation.ConsumeTransformed(ctx, original, delivered, options)
				} else {
					err = validation.Consume(ctx, delivered)
				}
				if err != nil {
					return err
				}
			}
			if compiled.Supports(DecodeEvent) {
				frame, err := compiled.DecodeFrame(ctx, wire, readback)
				if err != nil {
					return err
				}
				decoded = append(decoded, frame.Events...)
			}
			if compiled.packagePlan != nil {
				if err := validation.CommitDelivery(); err != nil {
					return err
				}
				if err := options.continuation.CommitDelivery(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if compiled.packagePlan == nil {
		err = consume(pending)
	} else {
		options = options.WithOutputStream(validation)
		err = encode(consume)
	}
	if err != nil {
		return nil, err
	}
	if err := validation.Finish(); err != nil {
		return nil, err
	}
	return decoded, options.continuation.Finish()
}
