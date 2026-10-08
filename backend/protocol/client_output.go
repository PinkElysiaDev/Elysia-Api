package protocol

// ParseClientOutput separates client presentation preferences from generation
// parameters. Legacy definitions opt in through the stream_options rule.
func ParseClientOutput(raw Value) (*ClientOutput, error) {
	if raw.IsZero() {
		return nil, nil
	}
	output := &ClientOutput{RawStreamOptions: raw}
	invalid := func(path string) error {
		return IssuesError([]ConversionIssue{{Code: InvalidInput, Severity: SeverityError, Direction: DecodeRequest, Stage: "decode", Path: path, Reason: "stream options must be an object or null; include_usage must be a boolean"}})
	}
	if raw.IsNull() {
		return output, nil
	}
	if !raw.IsObject() {
		return nil, invalid("/stream_options")
	}
	fields, err := raw.ReadObject()
	if err != nil {
		return nil, err
	}
	if value := fields["include_usage"]; !value.IsZero() {
		var enabled bool
		if value.IsNull() || value.Decode(&enabled) != nil {
			return nil, invalid("/stream_options/include_usage")
		}
		output.IncludeUsage = &enabled
	}
	return output, nil
}
