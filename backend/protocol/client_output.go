package protocol

func ValidateToolStreamOption(value Value, path string) error {
	if value.IsZero() || value.IsNull() {
		return nil
	}
	var enabled bool
	if value.Decode(&enabled) != nil {
		return streamIssue(InvalidInput, path, "tool input streaming preference must be a boolean or null")
	}
	return nil
}
