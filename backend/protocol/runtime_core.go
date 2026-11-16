package protocol

import (
	"context"
	"fmt"
)

func (compiled *Compiled) encodeEventValue(ctx context.Context, direction Direction, event Event, options EvaluationContext) (Value, error) {
	return encodeCore(ctx, compiled, direction, &event, options)
}

func decodeCore[T any](ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext) (*T, error) {
	if compiled.mappings[direction].hasCoreOutputPlan {
		return decodePlannedCore[T](ctx, compiled, direction, input, options)
	}
	return nil, compiled.rejectMissingMapping(ctx, direction)
}

func encodeCore[T any](ctx context.Context, compiled *Compiled, direction Direction, input *T, options EvaluationContext) (Value, error) {
	if compiled.mappings[direction].hasCoreInputPlan {
		return encodePlannedCore(ctx, compiled, direction, input, options)
	}
	// Invalid semantic input keeps priority over a missing direction, without
	// materializing JSON for an operation that cannot execute.
	limits := DefaultLimits()
	if isRequestDirection(direction) {
		limits = requestLimits(ctx, limits, false)
	}
	if err := checkSemanticValue(input, semanticLimits(limits)); err != nil {
		return Value{}, err
	}
	return Value{}, compiled.rejectMissingMapping(ctx, direction)
}

func (compiled *Compiled) decodeEventValues(ctx context.Context, direction Direction, frame Value, options EvaluationContext) ([]Event, error) {
	if compiled.mappings[direction].hasCoreOutputPlan {
		return decodePlannedEvents(ctx, compiled, direction, frame, options)
	}
	return nil, compiled.rejectMissingMapping(ctx, direction)
}

func (compiled *Compiled) rejectMissingMapping(ctx context.Context, direction Direction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return compiled.mappingError(direction, fmt.Errorf("direction is not implemented"))
}
