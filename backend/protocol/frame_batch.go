package protocol

// normalizeFrameBatch reproduces EncodeValue([]Value) followed by ReadArray:
// normalize raw spelling, preserve nil/absent errors, and enforce the aggregate
// array budget (including delimiters, commas, depth and repeated occurrences).
// The exact former error path still runs on failure. No placeholder JSON exists.
func normalizeFrameBatch(frames []Value, limits Limits) ([]Value, error) {
	fallback := func() ([]Value, error) {
		value, err := EncodeValue(frames)
		if err == nil {
			err = checkResourceValue(value, limits, "mapping_output")
		}
		if err != nil {
			return nil, err
		}
		return readArray(value)
	}
	if frames == nil {
		return fallback()
	}
	meter := typedMeter{limits: limits}
	if !meter.open() {
		return fallback()
	}
	for index, frame := range frames {
		if index > 0 {
			meter.bytes++
		}
		if !meter.raw(frame.raw) {
			return fallback()
		}
	}
	if !meter.close() {
		return fallback()
	}
	for index, frame := range frames {
		frames[index] = Value{raw: compactValueString(frame.raw)}
	}
	return frames, nil
}
