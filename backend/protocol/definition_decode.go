package protocol

import (
	"strings"
	"unicode/utf8"
)

// UnmarshalJSON expands compact reads and retains object-field presence so
// empty or false fields cannot evade operation validation.
func (expression *Expression) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var shorthand string
		if err := decodeContractText(text, &shorthand); err != nil {
			return err
		}
		decoded, err := parseCompactRead(shorthand)
		if err != nil {
			return err
		}
		*expression = decoded
		return nil
	}
	type plain Expression
	var decoded plain
	if err := decodeContractText(text, &decoded); err != nil {
		return err
	}
	*expression = Expression(decoded)
	if text == "null" {
		return nil
	}
	// The typed decoder already validated this object. Only keys are needed;
	// retaining RawMessages would copy each child subtree at every ancestor.
	for offset := skipJSONSpace(text, 1); text[offset] != '}'; {
		end := jsonStringEnd(text, offset)
		key := text[offset+1 : end-1]
		if strings.Contains(key, `\`) || !utf8.ValidString(key) {
			var err error
			key, err = decodeObjectKey(text[offset:end])
			if err != nil {
				return err
			}
		}
		if field := expressionFieldBits[key]; field != 0 {
			expression.present |= field
		} else {
			expression.unknownKeys = append(expression.unknownKeys, strings.Clone(key))
		}
		offset = skipJSONSpace(text, skipJSONSpace(text, end)+1)
		offset = skipJSONSpace(text, jsonValueEnd(text, offset))
		if text[offset] == ',' {
			offset = skipJSONSpace(text, offset+1)
		}
	}
	return nil
}
