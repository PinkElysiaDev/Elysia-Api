package protocol

import (
	"context"
	"errors"
	"maps"
	"strings"
)

// SourceFrame retains transport metadata and the original provider ordinal.
// Any corrected Value has passed the declared source normalization policy.
type SourceFrame struct {
	Value    Value
	Metadata Object
	Ordinal  int
}

// SourceEventState holds at most one opening frame before source validation.
// Its matching and corrections come entirely from the pinned source Mapping.
type SourceEventState struct {
	retention      streamReservation
	conversion     *CompiledConversion
	route          ConversionContext
	source         *Compiled
	sink           *DiagnosticSink
	limits         Limits
	rule           ConversionRule
	options        sourcePreludeOptions
	pending        *SourceFrame
	bytes, ordinal int
	isClosed       bool
	isReleased     bool
}

// NewSourceEventState binds source rules to one immutable source and route.
func NewSourceEventState(conversion *CompiledConversion, route ConversionContext, source *Compiled, sink *DiagnosticSink) *SourceEventState {
	limits := source.limits
	if conversion != nil {
		limits = intersectControlLimits(limits, conversion.limits)
	}
	return &SourceEventState{conversion: conversion, route: route, source: source, sink: sink, limits: limits, isClosed: conversion == nil || len(conversion.sourcePreludes) == 0}
}

// Push returns frames in provider order; a first match may wait for one frame.
// Rewriting occurs before the independent source lifecycle accepts either frame.
func (state *SourceEventState) Push(ctx context.Context, value Value, metadata Object) (frames []SourceFrame, err error) {
	defer func() {
		if err != nil {
			state.Finish(err)
			err = resourceIssue(err, state.source.identity, DecodeEvent, "conversion.source_event")
		}
	}()
	if state.isReleased {
		return nil, operationMetadataError("/state", "released source normalization cannot resume")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state.ordinal++
	frame := SourceFrame{Value: value, Metadata: metadata, Ordinal: state.ordinal}
	if state.isClosed {
		return []SourceFrame{frame}, nil
	}
	if state.pending == nil {
		return state.holdFirst(ctx, frame)
	}
	first := *state.pending
	state.clearPending()
	state.isClosed = true
	matched, err := state.matches(ctx, state.options.next, value)
	if err != nil || !matched {
		return []SourceFrame{first, frame}, err
	}
	evaluation := state.evaluation(ctx, value, first.Value)
	matched, err = evaluateSourcePreludePredicate(state.options.when, evaluation)
	if err != nil || !matched {
		return []SourceFrame{first, frame}, err
	}
	if err := state.validateFrame(ctx, value); err != nil {
		return nil, err
	}
	mutations, err := state.replacements(evaluation, first.Value)
	if err != nil {
		return nil, err
	}
	if len(mutations) == 0 {
		return []SourceFrame{first, frame}, nil
	}
	corrected, err := ApplyMutations(first.Value, mutations, state.limits)
	if err != nil {
		return nil, err
	}
	if err := state.validateFrame(ctx, corrected); err != nil {
		return nil, err
	}
	for _, mutation := range mutations {
		if err := state.conversion.controlDiagnostic(state.rule, state.route, controlDiagnosticInfo{Kind: "degrade", Path: mutation.Path, Attribution: "source"}, state.options.Prelude.Reason, state.sink); err != nil {
			return nil, err
		}
	}
	first.Value = corrected
	return []SourceFrame{first, frame}, nil
}

func (state *SourceEventState) holdFirst(ctx context.Context, frame SourceFrame) ([]SourceFrame, error) {
	limits := state.limits
	for _, rule := range state.conversion.sourcePreludes {
		if !rule.Match.matchesRoute(state.route) {
			continue
		}
		options := state.conversion.options[rule.ID].value.(sourcePreludeOptions)
		if options.Source != state.source.identity || state.route.Source != state.source.identity || options.DecoderHash != state.source.mappings[DecodeEvent].definitionHash {
			return nil, operationMetadataError("/sourcePrelude", "source normalization does not match the pinned decoder and route")
		}
		state.options = options
		state.limits = intersectControlLimits(limits, options.Limits)
		matched, err := state.matches(ctx, options.first, frame.Value)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if err := state.validateFrame(ctx, frame.Value); err != nil {
			return nil, err
		}
		metadata, err := EncodeValue(frame.Metadata)
		if err != nil {
			return nil, err
		}
		bytes := frame.Value.Len() + metadata.Len()
		if bytes > state.limits.BufferBytes {
			return nil, resourceFailure("source_prelude", "bytes", state.limits.BufferBytes, bytes)
		}
		if err := state.retention.check(1, bytes); err != nil {
			return nil, err
		}
		frame.Metadata = maps.Clone(frame.Metadata)
		for key, value := range frame.Metadata {
			frame.Metadata[key] = Value{raw: strings.Clone(value.raw)}
		}
		state.pending, state.rule, state.bytes = &frame, rule, bytes
		state.retention.commit(1, bytes)
		return nil, nil
	}
	state.limits = limits
	state.isClosed = true
	return []SourceFrame{frame}, nil
}

func (state *SourceEventState) matches(ctx context.Context, match compiledSourceFrameMatch, value Value) (bool, error) {
	if err := match.schema.validate(ctx, value, "/frame", state.limits); err != nil {
		var limit *ResourceLimitError
		if errors.As(err, &limit) || ctx.Err() != nil {
			return false, err
		}
		return false, nil
	}
	return evaluateSourcePreludePredicate(match.when, state.evaluation(ctx, value, value))
}

func evaluateSourcePreludePredicate(plan *compiledOutputPlan, state evaluation) (bool, error) {
	if plan == nil {
		return true, nil
	}
	value, err := plan.evaluate(state)
	if err != nil {
		return false, err
	}
	return readBoolean(value)
}

func (state *SourceEventState) evaluation(ctx context.Context, input, root Value) evaluation {
	return evaluation{ctx: ctx, input: input, root: root, valueReader: &valueReader{}, bindings: make([]JSONOutput, state.options.bindingSlots), budget: &evaluationBudget{limits: state.limits, stepLimit: state.limits.Nodes}}
}

func (state *SourceEventState) replacements(evaluation evaluation, first Value) ([]Mutation, error) {
	var updates []Mutation
	for _, update := range state.options.updates {
		value, err := update.value.evaluate(evaluation)
		if err != nil {
			return nil, err
		}
		previous, err := readValuePointer(first, update.pointer)
		if err != nil {
			return nil, err
		}
		if value.IsZero() || previous.IsZero() {
			return nil, operationMetadataError(update.path, "source prelude may only replace present fields with present values")
		}
		if !equalValues(previous, value) {
			updates = append(updates, Mutation{Op: ReplaceValue, Path: update.path, Value: value})
		}
	}
	return updates, nil
}

func (state *SourceEventState) validateFrame(ctx context.Context, frame Value) error {
	return state.source.validateSelectedWireContract(ctx, DecodeEvent, frame, nil, state.limits, state.source.selectFinalWireContract(DecodeEvent))
}

// Finish releases a pending original frame only on clean EOF.
// Transport errors and cancellation discard it without replacing their cause.
func (state *SourceEventState) Finish(readErr error) []SourceFrame {
	pending := state.pending
	state.clearPending()
	state.isClosed = true
	if pending != nil && readErr == nil {
		return []SourceFrame{*pending}
	}
	return nil
}

func (state *SourceEventState) clearPending() {
	state.pending, state.bytes = nil, 0
	state.retention.commit(0, 0)
}

// Release drops the bounded pending frame and ends this owner permanently.
func (state *SourceEventState) Release() {
	state.Finish(context.Canceled)
	state.isReleased = true
	state.retention.release()
}

func (state *SourceEventState) bindStreamBudget(budget *StreamBudget) error {
	if state.isReleased {
		return operationMetadataError("/state", "released source normalization cannot join a stream budget")
	}
	budget.constrain(state.limits)
	items := 0
	if state.pending != nil {
		items = 1
	}
	return state.retention.bind(budget, items, state.bytes)
}

// Sequence applies the same source normalization to a bounded preview array.
func (state *SourceEventState) Sequence(ctx context.Context, input Value) (Value, error) {
	frames, err := readArray(input)
	if err != nil || input.IsNull() {
		return Value{}, streamIssue(InvalidInput, "/input", "source_event requires an array of wire frames")
	}
	output := []Value{}
	bytes := 0
	appendFrames := func(batch []SourceFrame) error {
		for _, frame := range batch {
			bytes += frame.Value.Len()
			if bytes > state.limits.BufferBytes {
				return resourceFailure("source_prelude", "bytes", state.limits.BufferBytes, bytes)
			}
			output = append(output, frame.Value)
		}
		return nil
	}
	for _, frame := range frames {
		batch, err := state.Push(ctx, frame, nil)
		if err != nil {
			return Value{}, err
		}
		if err := appendFrames(batch); err != nil {
			state.Finish(err)
			return Value{}, err
		}
	}
	if err := appendFrames(state.Finish(nil)); err != nil {
		return Value{}, err
	}
	return EncodeValue(output)
}
