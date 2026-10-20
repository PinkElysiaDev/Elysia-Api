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
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := compiled.operations[name]; !exists {
		return compiled.runtimeError(DecodeRequest, fmt.Errorf("unknown operation %q", name))
	}
	validator := compiled.operationSchemas[name]
	if validator == nil {
		return nil
	}
	value, err := ParseValue(body)
	if err == nil {
		err = validator.validate(value, "/operation/input", compiled.RequestLimits(ctx))
	}
	return compiled.runtimeError(DecodeRequest, err)
}
