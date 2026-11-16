package protocol

import "context"

// WireOutputValidator enforces the declared client wire shape after all
// mappings and carrier insertion. Unknown optional extensions remain allowed.
type WireOutputValidator interface{ ValidateWireOutput(Direction, Value) error }

func (c *Compiled) ValidateWireOutput(direction Direction, value Value) error {
	return c.ValidateWireOutputContext(context.Background(), direction, value)
}

// ValidateWireOutputContext checks the final wire under this request's effective
// budget. Declarative contracts observe cancellation.
func (c *Compiled) ValidateWireOutputContext(ctx context.Context, direction Direction, value Value) error {
	return c.validateSelectedWireContract(ctx, direction, value, nil, c.wireLimits(ctx, direction), c.selectFinalWireContract(direction))
}

func (c *Compiled) validateWireOutput(direction Direction, value Value, fields Object) error {
	ctx := context.Background()
	return c.validateSelectedWireContract(ctx, direction, value, fields, c.wireLimits(ctx, direction), c.selectFinalWireContract(direction))
}

func (c *Compiled) validateWireContract(ctx context.Context, direction Direction, value Value, fields Object, effectiveLimits Limits) error {
	return c.validateSelectedWireContract(ctx, direction, value, fields, effectiveLimits, c.wireContracts[direction])
}

func (c *Compiled) validateSelectedWireContract(ctx context.Context, direction Direction, value Value, fields Object, effectiveLimits Limits, contract compiledWireContract) error {
	if contract.contextValidator != nil {
		return c.runtimeError(direction, contract.contextValidator.validate(ctx, direction, value, effectiveLimits))
	}
	if contract.validator != nil {
		if err := contract.validator.ValidateWireOutput(direction, value); err != nil {
			return c.runtimeError(direction, err)
		}
	}
	return nil
}
