package protocol

// MergeLogprobDeltas combines an initial probability value and ordered deltas
// with the same bytes as repeated node-metadata merging. It does not retain or
// mutate its inputs; unsupported shapes keep the existing merge behavior.
func MergeLogprobDeltas(initial Value, deltas []Value, codec string) Value {
	if len(deltas) == 0 {
		return initial
	}
	values := make([]Value, 0, len(deltas)+1)
	values = append(values, initial)
	values = append(values, deltas...)
	if codec == "responses" {
		var entries []Value
		for index, value := range values {
			var next []Value
			if err := value.Decode(&next); err != nil {
				return mergeLogprobDeltasSequentially(initial, deltas, codec)
			}
			if index == 0 {
				entries = next
			} else {
				entries = append(entries, next...)
			}
		}
		output, _ := EncodeValue(entries)
		return output
	}
	if codec != "openai-chat" {
		return mergeLogprobDeltasSequentially(initial, deltas, codec)
	}
	fields := make(map[string][]Value)
	for _, value := range values {
		if value.IsNull() {
			continue
		}
		object, err := value.ReadObject()
		if err != nil {
			return mergeLogprobDeltasSequentially(initial, deltas, codec)
		}
		for name, field := range object {
			var entries []Value
			if err := field.Decode(&entries); err != nil {
				return mergeLogprobDeltasSequentially(initial, deltas, codec)
			}
			if previous, exists := fields[name]; exists {
				fields[name] = append(previous, entries...)
			} else {
				fields[name] = entries
			}
		}
	}
	object := make(Object, len(fields))
	for name, entries := range fields {
		object[name], _ = EncodeValue(entries)
	}
	output, _ := EncodeValue(object)
	return output
}

func mergeLogprobDeltasSequentially(initial Value, deltas []Value, codec string) Value {
	for _, delta := range deltas {
		initial = appendLogprobValues(initial, delta, codec)
	}
	return initial
}

// A deferred value must be a known probability container. Token validation
// still belongs to its existing protocol boundary, including unknown fields.
func canDeferLogprobValue(value Value, codec string) bool {
	if value.IsNull() {
		return true
	}
	if codec == "responses" {
		return value.IsArray()
	}
	if codec != "openai-chat" || !value.IsObject() {
		return false
	}
	fields, err := value.ReadObject()
	if err != nil {
		return false
	}
	for _, field := range fields {
		if !field.IsArray() && !field.IsNull() {
			return false
		}
	}
	return true
}
