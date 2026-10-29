package protocol

import "context"

// WireOutputValidator enforces a codec's mandatory client wire shape after all
// mappings and carrier insertion. Unknown optional extensions remain allowed.
type WireOutputValidator interface{ ValidateWireOutput(Direction, Value) error }

// WireObjectOutputValidator accepts a caller-owned, read-only object parsed
// from the same immutable wire Value. It is an optional final-validation fast
// path; arbitrary modules retain the original validator contract.
type WireObjectOutputValidator interface {
	ValidateWireObjectOutput(Direction, Value, Object) error
}

func (c *Compiled) ValidateWireOutput(direction Direction, value Value) error {
	return c.ValidateWireOutputContext(context.Background(), direction, value)
}

// ValidateWireOutputContext checks the final wire under this request's effective
// budget. Declarative contracts observe cancellation; installed module contracts
// retain their existing validation behavior.
func (c *Compiled) ValidateWireOutputContext(ctx context.Context, direction Direction, value Value) error {
	return c.validateWireContract(ctx, direction, value, nil, c.wireLimits(ctx, direction))
}

func (c *Compiled) validateWireOutput(direction Direction, value Value, fields Object) error {
	ctx := context.Background()
	return c.validateWireContract(ctx, direction, value, fields, c.wireLimits(ctx, direction))
}

func (c *Compiled) validateWireContract(ctx context.Context, direction Direction, value Value, fields Object, effectiveLimits Limits) error {
	contract := c.wireContracts[direction]
	if contract.contextValidator != nil {
		return c.runtimeError(direction, contract.contextValidator.validate(ctx, direction, value, effectiveLimits))
	}
	if contract.objectValidator != nil && fields != nil {
		return c.runtimeError(direction, contract.objectValidator.ValidateWireObjectOutput(direction, value, fields))
	}
	if contract.validator != nil {
		if err := contract.validator.ValidateWireOutput(direction, value); err != nil {
			return c.runtimeError(direction, err)
		}
	}
	return nil
}
