package protocol

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
	return c.validateWireOutput(direction, value, nil)
}

func (c *Compiled) validateWireOutput(direction Direction, value Value, fields Object) error {
	module := c.mappings[direction].module
	if validator, ok := module.(WireObjectOutputValidator); ok && fields != nil {
		return c.runtimeError(direction, validator.ValidateWireObjectOutput(direction, value, fields))
	}
	if validator, ok := module.(WireOutputValidator); ok {
		if err := validator.ValidateWireOutput(direction, value); err != nil {
			return c.runtimeError(direction, err)
		}
	}
	return nil
}
