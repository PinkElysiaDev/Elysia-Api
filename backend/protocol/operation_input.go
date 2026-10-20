package protocol

// CheckOperationInput validates the wire contract of an operation returned by
// Operations. Shared decoding does not replace endpoint-specific requirements.
func (compiled *Compiled) CheckOperationInput(operation Operation, body []byte) error {
	if operation.Input == nil {
		return nil
	}
	value, err := ParseValue(body)
	if err == nil {
		var key string
		key, err = runtimeSchemaKey(operation.Input)
		if err == nil {
			validator, exists := compiled.operationSchemas[key]
			if !exists {
				// Operations returns customizable metadata. A changed input schema
				// must never accidentally reuse the pinned revision's validator.
				validator, err = compileValueSchema(operation.Input, compiled.limits)
			}
			if err == nil {
				err = validator.validate(value, "/operation/input", compiled.limits)
			}
		}
	}
	return compiled.runtimeError(DecodeRequest, err)
}
