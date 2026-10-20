package protocol

import (
	"context"
	"fmt"
)

// CheckOperationInput validates against the pinned revision's named operation.
// Schema changes belong in a new compiled definition, not request metadata.
// Wire validation uses the same pinned request budget as decoding and encoding,
// including any tighter limit explicitly authored by the protocol.
func (compiled *Compiled) CheckOperationInput(ctx context.Context, name string, body []byte) error {
	validator, err := compiled.operationInputSchema(ctx, name)
	if err != nil || validator == nil {
		return err
	}
	value, err := ParseValue(body)
	if err == nil {
		err = validator.validate(value, "/operation/input", compiled.RequestLimits(ctx))
	}
	return compiled.runtimeError(DecodeRequest, err)
}

// CheckOperationValueInput reuses syntax validation/ownership only. The pinned
// operation schema and the current request budget are evaluated on every call.
func (compiled *Compiled) CheckOperationValueInput(ctx context.Context, name string, value Value) error {
	validator, err := compiled.operationInputSchema(ctx, name)
	if err != nil || validator == nil {
		return err
	}
	if value.IsZero() {
		return compiled.runtimeError(DecodeRequest, fmt.Errorf("invalid JSON value"))
	}
	return compiled.runtimeError(DecodeRequest, validator.validate(value, "/operation/input", compiled.RequestLimits(ctx)))
}

func (compiled *Compiled) operationInputSchema(ctx context.Context, name string) (*runtimeValueSchema, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, exists := compiled.operations[name]; !exists {
		return nil, compiled.runtimeError(DecodeRequest, fmt.Errorf("unknown operation %q", name))
	}
	return compiled.operationSchemas[name], nil
}
