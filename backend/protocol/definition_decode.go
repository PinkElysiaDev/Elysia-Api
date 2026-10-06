package protocol

import "encoding/json"

// UnmarshalJSON rejects unknown expression keys and retains the presence of
// empty/false fields so an unrelated key cannot evade operation validation.
func (expression *Expression) UnmarshalJSON(raw []byte) error {
	type plain Expression
	var decoded plain
	if err := decodeContract(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*expression = Expression(decoded)
	expression.present = make(map[string]bool, len(fields))
	for key := range fields {
		expression.present[key] = true
	}
	return nil
}
