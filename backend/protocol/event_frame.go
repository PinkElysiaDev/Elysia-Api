package protocol

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// EventFrame groups the semantic events produced by one wire frame. Native
// bytes are retained once, so a multi-item chunk never replays several times.
type EventFrame struct {
	Events      []Event
	native      *Native
	digest      [sha256.Size]byte
	encoderHash string
	decoderHash string
}

// DecodeFrame uses request-owned module state and preserves an unchanged native
// frame independently of how many semantic events it contains.
func (compiled *Compiled) DecodeFrame(ctx context.Context, frame Value, options EvaluationContext) (*EventFrame, error) {
	return compiled.decodeFrame(ctx, DecodeEvent, frame, options)
}

func (compiled *Compiled) decodeFrame(ctx context.Context, direction Direction, frame Value, options EvaluationContext) (*EventFrame, error) {
	options.compoundFrame = true
	events, err := compiled.decodeEvents(ctx, direction, frame, options)
	if err != nil {
		return nil, err
	}
	result := &EventFrame{Events: events, encoderHash: compiled.mappings[eventEncoder(direction)].definitionHash, decoderHash: compiled.mappings[direction].definitionHash}
	if compiled.native.Preserve {
		result.native = &Native{Source: Provenance{Protocol: compiled.identity, Direction: direction, Scope: options.Scope}, Value: frame}
		encoded, err := EncodeValue(events)
		if err != nil {
			return nil, err
		}
		result.digest = sha256.Sum256(encoded.Bytes())
	}
	return result, nil
}

// EncodeFrames expands the explicitly declared frame batch. An empty batch is
// a codec control event, never a fabricated terminal or an ignored failure.
func (compiled *Compiled) EncodeFrames(ctx context.Context, event Event, options EvaluationContext) ([]Value, error) {
	return compiled.encodeFrames(ctx, EncodeEvent, event, options)
}

func (compiled *Compiled) encodeFrames(ctx context.Context, direction Direction, event Event, options EvaluationContext) ([]Value, error) {
	value, err := compiled.encodeEvent(ctx, direction, event, options)
	if err != nil {
		return nil, err
	}
	if !compiled.mappings[direction].frameBatch {
		return []Value{value}, nil
	}
	return readArray(value)
}

// EncodeFrame applies the target codec once per semantic event. Same-wire
// unmodified frames replay exactly once after target capability validation.
func (compiled *Compiled) EncodeFrame(ctx context.Context, frame *EventFrame, options EvaluationContext) ([]Value, error) {
	return compiled.encodeFrame(ctx, EncodeEvent, frame, options)
}

func (compiled *Compiled) encodeFrame(ctx context.Context, direction Direction, frame *EventFrame, options EvaluationContext) ([]Value, error) {
	target := compiled.target(direction, options)
	canReplay := compiled.native.Preserve && frame.native != nil && CanPreserveNative(frame.native.Source, target)
	if canReplay {
		if frame.encoderHash == "" || frame.encoderHash != compiled.mappings[direction].definitionHash || frame.decoderHash != compiled.mappings[eventDecoder(direction)].definitionHash {
			return nil, streamIssue(UnsupportedNative, "/frame", "native frame replay requires equivalent event encoders and decoders; an explicit mapping cannot be bypassed")
		}
		if !CheckScope(frame.native.Source.Scope, target.Scope) {
			return nil, streamIssue(ResourceScopeMismatch, "/frame", "native frame scope differs from target")
		}
		encoded, err := EncodeValue(frame.Events)
		if err != nil {
			return nil, err
		}
		if frame.digest != sha256.Sum256(encoded.Bytes()) {
			mapping := compiled.mappings[direction]
			if len(frame.Events) == 1 && mapping.module == nil && !mapping.frameBatch {
				event := frame.Events[0]
				event.Native = frame.native
				return compiled.encodeFrames(ctx, direction, event, options)
			}
			return nil, fmt.Errorf("modified compound native frame requires explicit wire mutations")
		}
	}
	var frames []Value
	for _, event := range frame.Events {
		if err := IssuesError(CheckEvent(event, target, compiled.limits)); err != nil {
			return nil, err
		}
		// Pinned same-wire streams remain native throughout; source decoding and
		// replay validate the lifecycle before any original frame is emitted.
		if canReplay {
			continue
		}
		value, err := compiled.encodeFrames(ctx, direction, event, options)
		if err != nil {
			return nil, err
		}
		frames = append(frames, value...)
	}
	if canReplay {
		return []Value{frame.native.Value}, nil
	}
	return frames, nil
}
