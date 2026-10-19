package protocol

type conversionOption struct {
	raw   Value
	value any
	err   error
}

func parseConversionOption[T any](raw Value) conversionOption {
	var value T
	err := raw.Decode(&value)
	return conversionOption{raw: raw, value: value, err: err}
}

// Compiled options are immutable. A copied/overridden rule only uses the cache
// when its exact value and expected type still agree; errors stay at execution.
func readConversionOption[T any](c *CompiledConversion, rule ConversionRule) (T, error) {
	if c != nil {
		if option, ok := c.options[rule.ID]; ok && option.raw == rule.Value {
			if value, ok := option.value.(T); ok {
				return value, option.err
			}
		}
	}
	var value T
	err := rule.Value.Decode(&value)
	return value, err
}
