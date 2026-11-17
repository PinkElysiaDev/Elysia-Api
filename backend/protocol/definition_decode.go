package protocol

import (
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic/decoder"
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
	if decoded, ok := decodeExpressionFields(text); ok {
		*expression = decoded
		return nil
	}
	// Keep the strict codec's exact diagnostics and case-insensitive decoding
	// contract on malformed/unknown/mixed-case fields. This path is cold; valid
	// expressions never rescan child subtrees to discover field presence.
	type plain Expression
	var decoded plain
	if err := decodeContractText(text, &decoded); err != nil {
		return err
	}
	*expression = Expression(decoded)
	if text == "null" {
		return nil
	}
	for offset := skipJSONSpace(text, 1); text[offset] != '}'; {
		end := jsonStringEnd(text, offset)
		key, err := decodeObjectKey(text[offset:end])
		if err != nil {
			return err
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

func decodeExpressionFields(text string) (Expression, bool) {
	var expression Expression
	if len(text) < 2 || text[0] != '{' {
		return expression, false
	}
	reader := decoder.NewDecoder("")
	reader.SetOptions(decoder.OptionDisableUnknown | decoder.OptionUseNumber | decoder.OptionCopyString | decoder.OptionValidateString)
	for offset := skipJSONSpace(text, 1); offset < len(text); {
		if text[offset] == '}' {
			return expression, offset == len(text)-1
		}
		if text[offset] != '"' {
			break
		}
		end := jsonStringEnd(text, offset)
		if end > len(text) || end <= offset+1 {
			break
		}
		key := text[offset+1 : end-1]
		if strings.Contains(key, `\`) || !utf8.ValidString(key) {
			var err error
			key, err = decodeObjectKey(text[offset:end])
			if err != nil {
				break
			}
		}
		target := expression.fieldTarget(key)
		if target == nil {
			break
		}
		expression.present |= expressionFieldBits[key]
		offset = skipJSONSpace(text, end)
		if offset >= len(text) || text[offset] != ':' {
			break
		}
		offset = skipJSONSpace(text, offset+1)
		reader.Reset(text[offset:])
		if err := reader.Decode(target); err != nil {
			break
		}
		offset = skipJSONSpace(text, offset+reader.Pos())
		if offset >= len(text) {
			break
		}
		if text[offset] == '}' {
			return expression, offset == len(text)-1
		}
		if text[offset] != ',' {
			break
		}
		offset = skipJSONSpace(text, offset+1)
		if offset >= len(text) || text[offset] == '}' {
			break
		}
	}
	return Expression{}, false
}

func (expression *Expression) fieldTarget(key string) any {
	switch key {
	case "op":
		return &expression.Op
	case "operation":
		return &expression.Operation
	case "name":
		return &expression.Name
	case "namespace":
		return &expression.Namespace
	case "keyMode":
		return &expression.KeyMode
	case "from":
		return &expression.From
	case "path":
		return &expression.Path
	case "value":
		return &expression.Value
	case "fields":
		return &expression.Fields
	case "items":
		return &expression.Items
	case "source":
		return &expression.Source
	case "body":
		return &expression.Body
	case "when":
		return &expression.When
	case "then":
		return &expression.Then
	case "otherwise":
		return &expression.Otherwise
	case "cases":
		return &expression.Cases
	case "values":
		return &expression.Values
	case "to":
		return &expression.To
	case "key":
		return &expression.Key
	case "keys":
		return &expression.Keys
	case "prefixes":
		return &expression.Prefixes
	case "policy":
		return &expression.Policy
	case "onConflict":
		return &expression.OnConflict
	case "onUnknown":
		return &expression.OnUnknown
	case "diagnostic":
		return &expression.Diagnostic
	case "reason":
		return &expression.Reason
	case "ref":
		return &expression.Ref
	case "required":
		return &expression.Required
	default:
		return nil
	}
}
