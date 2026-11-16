package protocol

import (
	"context"
	"fmt"
)

// FinishEvents flushes an event encoder that actually encoded semantic events.
// Native observation alone must not produce another terminal.
func (compiled *Compiled) FinishEvents(ctx context.Context, options EvaluationContext) ([]Value, error) {
	return compiled.finishEvents(ctx, EncodeEvent, options)
}

// FinishInputEvents validates EOF for the source stream owned by DecodeFrame.
// An explicit WireStreamValidation instead finishes through its own Finish.
func (compiled *Compiled) FinishInputEvents(ctx context.Context, options EvaluationContext) error {
	return compiled.finishInputEvents(ctx, DecodeEvent, options)
}

func (compiled *Compiled) finishInputEvents(ctx context.Context, direction Direction, options EvaluationContext) error {
	if compiled.wireContracts[direction].packageStream == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.State == nil {
		return fmt.Errorf("source stream finalization requires its EvaluationState")
	}
	key := sourceStreamKey{executionStateKey: executionStateKey{compiled: compiled, direction: direction}, scope: options.Scope}
	validator := options.State.sourceStreams[key]
	if validator == nil {
		var err error
		validator, err = compiled.newWireStreamValidation(direction, options.Scope)
		if err != nil {
			return err
		}
	}
	return compiled.runtimeError(direction, validator.Finish())
}

func (compiled *Compiled) finishEvents(ctx context.Context, direction Direction, options EvaluationContext) ([]Value, error) {
	if options.State == nil {
		return nil, nil
	}
	return compiled.finishPackageEvents(ctx, direction, options)
}

type executionStateKey struct {
	compiled  *Compiled
	direction Direction
}

type sourceStreamKey struct {
	executionStateKey
	scope Scope
}

// EvaluationState owns declarative execution state for one stream/session. It is not
// shared across requests or goroutines and is inaccessible to expressions.
type EvaluationState struct {
	retention         streamReservation
	packageStates     map[executionStateKey]*packageStreamState
	packageStateItems int
	packageStateBytes int
	sourceStreams     map[sourceStreamKey]*WireStreamValidation
}

// NewEvaluationState creates independent stream state. Declarative expressions
// remain immutable; association and terminal validation belong to EventReplay.
func NewEvaluationState() *EvaluationState {
	return &EvaluationState{}
}
