package protocol

import (
	"fmt"
	"math/bits"
)

type expressionFieldMask uint32

var expressionFieldNames = [...]string{
	"op", "operation", "name", "namespace", "keyMode", "from", "path", "value",
	"fields", "items", "source", "body", "when", "then", "otherwise", "cases",
	"values", "to", "key", "keys", "prefixes", "policy", "onConflict", "onUnknown",
	"diagnostic", "reason", "ref", "required",
}

var expressionFieldBits = func() map[string]expressionFieldMask {
	fields := make(map[string]expressionFieldMask, len(expressionFieldNames))
	for index, name := range expressionFieldNames {
		fields[name] = 1 << index
	}
	return fields
}()

type expressionFieldContract struct{ allowed, required expressionFieldMask }

var expressionFieldContracts = func() map[string]expressionFieldContract {
	contracts := make(map[string]expressionFieldContract, len(expressionOperations))
	for _, operation := range expressionOperations {
		contract := expressionFieldContract{allowed: expressionFieldBits["op"]}
		for _, name := range operation.Fields {
			contract.allowed |= expressionFieldBits[name]
		}
		for _, name := range operation.Required {
			contract.required |= expressionFieldBits[name]
		}
		contracts[operation.Name] = contract
	}
	return contracts
}()

func (fields expressionFieldMask) has(name string) bool { return fields&expressionFieldBits[name] != 0 }

// Presence in JSON cannot stand in for the current Go value: callers can build
// or edit expressions after decoding. Empty omitzero slices still count.
func (expression *Expression) valueFields() expressionFieldMask {
	values := [...]bool{
		true, expression.Operation != "", expression.Name != "", expression.Namespace != "",
		expression.KeyMode != "", expression.From != "", expression.Path != "", !expression.Value.IsZero(),
		len(expression.Fields) != 0, len(expression.Items) != 0, expression.Source != nil, expression.Body != nil,
		expression.When != nil, expression.Then != nil, expression.Otherwise != nil, len(expression.Cases) != 0,
		len(expression.Values) != 0, expression.To != "", expression.Key != "", expression.Keys != nil,
		expression.Prefixes != nil, expression.Policy != "", expression.OnConflict != nil, expression.OnUnknown != nil,
		expression.Diagnostic != nil, expression.Reason != "", expression.Ref != "", expression.Required,
	}
	var fields expressionFieldMask
	for index, hasValue := range values {
		if hasValue {
			fields |= 1 << index
		}
	}
	return fields
}

func checkExpressionFields(expression Expression, operation OperationInfo, path string) error {
	if len(expression.unknownKeys) != 0 {
		return fmt.Errorf("%s/%s: field is not allowed for %s", path, expression.unknownKeys[0], operation.Name)
	}
	contract := expressionFieldContracts[operation.Name]
	value := expression.valueFields()
	for _, fields := range [...]expressionFieldMask{expression.present, value} {
		if forbidden := fields &^ contract.allowed; forbidden != 0 {
			key := expressionFieldNames[bits.TrailingZeros32(uint32(forbidden))]
			return fmt.Errorf("%s/%s: field is not allowed for %s", path, key, operation.Name)
		}
	}
	if missing := contract.required &^ value; missing != 0 {
		for _, key := range operation.Required {
			if missing.has(key) {
				return fmt.Errorf("%s/%s is required for %s", path, key, operation.Name)
			}
		}
	}
	// A zero Value is Go-only absence, never a valid literal enum member.
	for key, member := range expression.Values {
		if member.IsZero() {
			return fmt.Errorf("%s/values/%s: absent JSON value requires omission", path, escapePointer(key))
		}
	}
	return nil
}
