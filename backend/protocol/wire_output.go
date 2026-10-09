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

// Legacy fixtures omitted nullable stop fields. Only these empty protocol
// markers are equivalent; counters and all unknown extensions remain exact.
func (c *Compiled) comparableWireFixture(direction Direction, value Value) Value {
	if c.Codec(direction) != "anthropic" || (direction != EncodeEvent && direction != EncodeResponse) {
		return value
	}
	if direction == EncodeResponse {
		fields, err := value.ReadObject()
		if err == nil && fields["stop_sequence"].IsNull() {
			delete(fields, "stop_sequence")
			value, _ = EncodeValue(fields)
		}
		return value
	}
	var frames []Value
	if value.Decode(&frames) != nil {
		return value
	}
	for i, frame := range frames {
		fields, err := frame.ReadObject()
		if err != nil {
			continue
		}
		key := ""
		switch fields["type"] {
		case StringValue("message_start"):
			key = "message"
		case StringValue("message_delta"):
			key = "delta"
		}
		if key == "" {
			continue
		}
		object, err := fields[key].ReadObject()
		if err != nil {
			continue
		}
		for _, name := range []string{"stop_reason", "stop_sequence"} {
			if object[name].IsNull() {
				delete(object, name)
			}
		}
		fields[key], _ = EncodeValue(object)
		frames[i], _ = EncodeValue(fields)
	}
	output, _ := EncodeValue(frames)
	return output
}
