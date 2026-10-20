package protocol

import "fmt"

// CheckOperationInput validates against the pinned revision's named operation.
// Schema changes belong in a new compiled definition, not request metadata.
func (compiled *Compiled) CheckOperationInput(name string, body []byte) error {
	if _, exists := compiled.operations[name]; !exists {
		return compiled.runtimeError(DecodeRequest, fmt.Errorf("unknown operation %q", name))
	}
	validator := compiled.operationSchemas[name]
	if validator == nil {
		return nil
	}
	value, err := ParseValue(body)
	if err == nil {
		err = validator.validate(value, "/operation/input", compiled.limits)
	}
	return compiled.runtimeError(DecodeRequest, err)
}
