package protocol

// WireOutputValidator enforces a codec's mandatory client wire shape after all
// mappings and carrier insertion. Unknown optional extensions remain allowed.
type WireOutputValidator interface{ ValidateWireOutput(Direction, Value) error }

func (c *Compiled) ValidateWireOutput(direction Direction, value Value) error {
	module := c.mappings[direction].module
	if validator, ok := module.(WireOutputValidator); ok {
		if err := validator.ValidateWireOutput(direction, value); err != nil {
			return c.runtimeError(direction, err)
		}
	}
	return nil
}
