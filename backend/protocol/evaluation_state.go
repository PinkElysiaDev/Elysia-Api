package protocol

import (
	"context"
	"fmt"
)

// StreamModule creates a bounded, single-owner module instance for one event
// direction. Registry modules never hold per-request mutable state.
type StreamModule interface {
	Module
	NewStream(Direction, Limits) (Module, error)
}

// StreamFinalizer emits a deferred terminal after all usage tail frames. It
// cannot create semantic success; EventReplay must already have accepted it.
type StreamFinalizer interface {
	Finish(context.Context, EvaluationContext) ([]Value, error)
}

// NativeFrameObserver synchronizes framing state without rebuilding a replayed
// frame. Observers must not interpret opaque extensions or synthesize content.
// A native-only observer is never finalized: source/target replay already check
// that stream's terminal, and no semantic encoder terminal is pending.
type NativeFrameObserver interface {
	ObserveNativeFrame(context.Context, Value, EvaluationContext) error
}

// FinishEvents flushes an event encoder that actually encoded semantic events.
// Native observation alone must not produce another terminal.
func (compiled *Compiled) FinishEvents(ctx context.Context, options EvaluationContext) ([]Value, error) {
	return compiled.finishEvents(ctx, EncodeEvent, options)
}

func (compiled *Compiled) finishEvents(ctx context.Context, direction Direction, options EvaluationContext) ([]Value, error) {
	if options.State == nil {
		return nil, nil
	}
	instance := options.State.modules[moduleStateKey{compiled: compiled, direction: direction}]
	if instance == nil || !instance.executed {
		return nil, nil
	}
	finalizer, hasFinalizer := instance.module.(StreamFinalizer)
	if !hasFinalizer {
		return nil, nil
	}
	options = options.forDefinition(compiled)
	return finalizer.Finish(ctx, options)
}

type moduleStateKey struct {
	compiled  *Compiled
	direction Direction
}

type streamModuleState struct {
	module   Module
	executed bool
}

// EvaluationState owns native codec state for one stream/session. It is not
// shared across requests or goroutines and is inaccessible to expressions.
type EvaluationState struct {
	modules     map[moduleStateKey]*streamModuleState
	initialized map[moduleStateKey]bool
}

// NewEvaluationState creates independent codec state. Declarative expressions
// remain immutable; association and terminal validation belong to EventReplay.
func NewEvaluationState() *EvaluationState {
	return &EvaluationState{modules: map[moduleStateKey]*streamModuleState{}, initialized: map[moduleStateKey]bool{}}
}

func (state *EvaluationState) prependInitial(compiled *Compiled, direction Direction, initial *compiledExpression, evaluation evaluation, output Value) (Value, bool, error) {
	if state == nil {
		return Value{}, false, fmt.Errorf("initial events require one EvaluationState per stream")
	}
	key := moduleStateKey{compiled: compiled, direction: direction}
	if state.initialized[key] {
		return output, false, nil
	}
	if len(state.initialized)+len(state.modules) >= compiled.limits.StateItems {
		return Value{}, false, fmt.Errorf("event mapping state exceeds the item limit")
	}
	events, err := eventValues(output)
	if err != nil || len(events) == 0 {
		return output, false, err
	}
	prefix, err := initial.evaluate(evaluation)
	if err != nil {
		return Value{}, false, err
	}
	values, err := eventValues(prefix)
	if err != nil {
		return Value{}, false, err
	}
	combined, err := EncodeValue(append(values, events...))
	return combined, true, err
}

func eventValues(value Value) ([]Value, error) {
	if value.IsObject() {
		return []Value{value}, nil
	}
	return readArray(value)
}

func (state *EvaluationState) resolve(compiled *Compiled, direction Direction, module Module) (Module, error) {
	return state.resolveModule(compiled, direction, module, true)
}

func (state *EvaluationState) observeNative(ctx context.Context, compiled *Compiled, direction Direction, frame Value, options EvaluationContext) error {
	module := compiled.mappings[direction].module
	if _, ok := module.(StreamModule); !ok {
		return nil
	}
	instance, err := state.resolveModule(compiled, direction, module, false)
	if err != nil {
		return err
	}
	if observer, ok := instance.(NativeFrameObserver); ok {
		return observer.ObserveNativeFrame(ctx, frame, options.forDefinition(compiled))
	}
	return nil
}

func (state *EvaluationState) resolveModule(compiled *Compiled, direction Direction, module Module, executed bool) (Module, error) {
	factory, isStateful := module.(StreamModule)
	if !isStateful || !isEventDirection(direction) {
		return module, nil
	}
	if state == nil {
		return nil, fmt.Errorf("stateful event adapter requires one EvaluationState per stream")
	}
	key := moduleStateKey{compiled: compiled, direction: direction}
	if current, exists := state.modules[key]; exists {
		current.executed = current.executed || executed
		return current.module, nil
	}
	if len(state.initialized)+len(state.modules) >= compiled.limits.StateItems {
		return nil, fmt.Errorf("event mapping state exceeds the item limit")
	}
	current, err := factory.NewStream(direction, compiled.limits)
	if err != nil {
		return nil, err
	}
	state.modules[key] = &streamModuleState{module: current, executed: executed}
	return current, nil
}
