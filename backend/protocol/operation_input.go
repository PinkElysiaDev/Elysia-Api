package protocol

// CheckOperationInput validates the wire contract of an operation returned by
// Operations. Shared decoding does not replace endpoint-specific requirements.
func (compiled *Compiled) CheckOperationInput(operation Operation, body []byte) error {
	if operation.Input == nil {
		return nil
	}
	value, err := ParseValue(body)
	if err == nil {
		err = checkValueSchema(value, operation.Input, "/operation/input", 1, compiled.limits)
	}
	return compiled.runtimeError(DecodeRequest, err)
}
