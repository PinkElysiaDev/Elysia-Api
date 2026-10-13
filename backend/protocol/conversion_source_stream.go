package protocol

import (
	"context"
	"fmt"
	"slices"
)

// SourceFrame retains the transport metadata and ordinal of a provider frame.
// Value may contain an explicitly diagnosed startup identity correction.
type SourceFrame struct {
	Value    Value
	Metadata Object
	Ordinal  int
}

// SourceEventState delays at most one empty Chat startup frame, before either
// the decoder or native replay commits its response identity. It is request owned.
type SourceEventState struct {
	conversion *CompiledConversion
	route      ConversionContext
	sink       *DiagnosticSink
	limits     Limits
	rule       *ConversionRule
	pending    *SourceFrame
	ordinal    int
	isClosed   bool
}

// NewSourceEventState enables compatibility only for actual installed codecs.
func NewSourceEventState(conversion *CompiledConversion, route ConversionContext, source, target *Compiled, sink *DiagnosticSink) *SourceEventState {
	state := &SourceEventState{conversion: conversion, route: route, sink: sink, limits: DefaultLimits()}
	state.isClosed = conversion == nil || source.Codec(DecodeEvent) != "openai-chat" || !knownConversionCodec(target.Codec(EncodeEvent))
	if !state.isClosed {
		state.limits = conversion.limits
		state.limits.BufferBytes = min(state.limits.BufferBytes, source.ResourceLimits().BufferBytes)
	}
	return state
}

// Push returns zero, one or two frames in provider order. Nonmatching streams
// pass unchanged to the ordinary source contract; this is not a general repair.
func (state *SourceEventState) Push(ctx context.Context, value Value, metadata Object) ([]SourceFrame, error) {
	if err := ctx.Err(); err != nil {
		state.Finish(err)
		return nil, err
	}
	state.ordinal++
	frame := SourceFrame{Value: value, Metadata: metadata, Ordinal: state.ordinal}
	if state.isClosed {
		return []SourceFrame{frame}, nil
	}
	if err := checkValueLimits(value, state.limits); err != nil {
		state.Finish(err)
		return nil, streamIssue(LimitExceeded, "/frame", err.Error())
	}
	if state.pending == nil {
		for _, rule := range state.conversion.Policy.Rules {
			if rule.Enabled && rule.Phase == ConversionSourceEvent && rule.Action == "chat_stream_identity" && rule.Match.matches(state.route, value) {
				state.rule = &rule
				break
			}
		}
		if _, ok := chatStartupIdentity(value, true); state.rule == nil || !ok {
			state.isClosed = true
			return []SourceFrame{frame}, nil
		}
		// Metadata can outlive the ReadFrames callback while the prelude waits.
		encodedMetadata, err := EncodeValue(metadata)
		if err != nil || len(value.raw)+len(encodedMetadata.raw) > state.limits.BufferBytes {
			state.isClosed = true
			return nil, streamIssue(LimitExceeded, "/frame", "startup frame and transport metadata exceed buffer limit")
		}
		frame.Metadata = copyObject(metadata)
		state.pending = &frame
		return nil, nil
	}
	first := *state.pending
	state.pending, state.isClosed = nil, true
	initial, _ := chatStartupIdentity(first.Value, true)
	next, isStartup := chatStartupIdentity(value, false)
	if !isStartup || initial.model != next.model || initial.id == next.id {
		return []SourceFrame{first, frame}, nil
	}
	mutations := []Mutation{{Op: ReplaceValue, Path: "/id", Value: next.fields["id"]}}
	if initial.created != next.created {
		mutations = append(mutations, Mutation{Op: ReplaceValue, Path: "/created", Value: next.fields["created"]})
	}
	corrected, err := ApplyMutations(first.Value, mutations, state.limits)
	if err != nil {
		return nil, streamIssue(LimitExceeded, "/frame", err.Error())
	}
	for _, mutation := range mutations {
		if err := state.conversion.issue(*state.rule, ConversionSourceEvent, state.route, mutation.Path,
			fmt.Sprintf("provider frame %d: empty startup identity aligned with frame %d before delivery", first.Ordinal, frame.Ordinal), state.sink, true); err != nil {
			return nil, err
		}
	}
	first.Value = corrected
	return []SourceFrame{first, frame}, nil
}

// Finish releases a pending prelude only on clean EOF. Failures and cancellation
// discard it so flushing cannot replace the actual transport failure.
func (state *SourceEventState) Finish(readErr error) []SourceFrame {
	pending := state.pending
	state.pending, state.isClosed = nil, true
	if pending != nil && readErr == nil {
		return []SourceFrame{*pending}
	}
	return nil
}

// Sequence uses the same bounded processor for previews and combination proofs.
// The returned array contains wire frames, not decoded semantic events.
func (state *SourceEventState) Sequence(ctx context.Context, input Value) (Value, error) {
	frames, err := input.readArray()
	if err != nil || input.IsNull() {
		return Value{}, streamIssue(InvalidInput, "/input", "source_event requires an array of wire frames")
	}
	var output []Value
	buffered := 0
	appendFrames := func(batch []SourceFrame) error {
		for _, frame := range batch {
			buffered += len(frame.Value.raw)
			if buffered > state.limits.BufferBytes {
				return streamIssue(LimitExceeded, "/input", "source event sequence exceeds preview buffer limit")
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
	if output == nil {
		output = []Value{}
	}
	return EncodeValue(output)
}

type chatStartup struct {
	id, model string
	created   int64
	fields    Object
}

func chatStartupIdentity(value Value, isFirst bool) (chatStartup, bool) {
	fields, err := value.ReadObject()
	if err != nil {
		return chatStartup{}, false
	}
	allowed := []string{"id", "model", "created", "object", "choices", "usage", "system_fingerprint"}
	if !isFirst {
		allowed = append(allowed, "service_tier")
	}
	identity := chatStartup{fields: fields}
	var object string
	if !hasOnlyFields(fields, allowed...) || fields["object"].Decode(&object) != nil || object != "chat.completion.chunk" ||
		fields["id"].Decode(&identity.id) != nil || identity.id == "" ||
		fields["model"].Decode(&identity.model) != nil || identity.model == "" ||
		fields["created"].IsZero() || fields["created"].IsNull() || fields["created"].Decode(&identity.created) != nil || identity.created < 0 ||
		!isAbsentOrNull(fields["usage"]) || !isAbsentOrNull(fields["system_fingerprint"]) {
		return chatStartup{}, false
	}
	if tier := fields["service_tier"]; !tier.IsZero() {
		if err := ValidateMetadataValue("openai-chat", "response", "service_tier", tier, "/service_tier"); err != nil {
			return chatStartup{}, false
		}
	}
	choices, err := fields["choices"].readArray()
	if err != nil || len(choices) != 1 {
		return chatStartup{}, false
	}
	choice, err := choices[0].ReadObject()
	var index int
	if err != nil || !hasOnlyFields(choice, "index", "delta", "finish_reason", "logprobs") ||
		choice["index"].IsZero() || choice["index"].IsNull() || choice["index"].Decode(&index) != nil || index != 0 ||
		!isAbsentOrNull(choice["finish_reason"]) || !isAbsentOrNull(choice["logprobs"]) {
		return chatStartup{}, false
	}
	delta, err := choice["delta"].ReadObject()
	var role string
	if err != nil || !hasOnlyFields(delta, "role", "content") || delta["role"].Decode(&role) != nil || role != "assistant" ||
		(delta["content"] != StringValue("") && (isFirst || !delta["content"].IsZero())) {
		return chatStartup{}, false
	}
	return identity, true
}

func hasOnlyFields(fields Object, allowed ...string) bool {
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return false
		}
	}
	return true
}

func isAbsentOrNull(value Value) bool { return value.IsZero() || value.IsNull() }
