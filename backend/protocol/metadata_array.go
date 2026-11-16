package protocol

// Array fragments stay independent until the consumer requests a snapshot.
func mergeArrayDeltas(initial Value, deltas []Value) (Value, error) {
	var entries []Value
	if err := initial.Decode(&entries); err != nil {
		return Value{}, err
	}
	for _, value := range deltas {
		var next []Value
		if err := value.Decode(&next); err != nil {
			return Value{}, err
		}
		entries = append(entries, next...)
	}
	return EncodeValue(entries)
}
