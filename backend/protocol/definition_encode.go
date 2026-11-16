package protocol

import (
	"slices"

	"github.com/bytedance/sonic"
)

//go:generate go run ../internal/expressionencode

const (
	expressionJSONCapacity   = 256
	expressionMapKeyCapacity = 16
)

// Expression children share one append buffer; only opaque leaves use the codec.
type expressionJSONWriter struct {
	bytes []byte
	err   error
}

// MarshalJSON preserves the authored representation without encoding each child
// into a temporary JSON buffer. Compact-origin edits retain strict field checks.
func (expression Expression) MarshalJSON() ([]byte, error) {
	writer := expressionJSONWriter{bytes: make([]byte, 0, expressionJSONCapacity)}
	writer.appendExpression(expression)
	if writer.err != nil {
		return nil, writer.err
	}
	return writer.bytes, nil
}

func (writer *expressionJSONWriter) appendExpression(expression Expression) {
	if writer.err != nil {
		return
	}
	if expression.isCompactRead {
		if err := expression.checkCompactFields(); err != nil {
			writer.err = err
			return
		}
		if shorthand, canCompact := expression.formatCompactRead(); canCompact {
			writer.appendString(shorthand)
			return
		}
	}
	writer.appendObject(expression)
}

func (writer *expressionJSONWriter) appendExpressionMap(expressions map[string]Expression) {
	var storage [expressionMapKeyCapacity]string
	keys := storage[:0]
	for key := range expressions {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	writer.bytes = append(writer.bytes, '{')
	for index, key := range keys {
		if index > 0 {
			writer.bytes = append(writer.bytes, ',')
		}
		writer.appendString(key)
		writer.bytes = append(writer.bytes, ':')
		writer.appendExpression(expressions[key])
	}
	writer.bytes = append(writer.bytes, '}')
}

func (writer *expressionJSONWriter) appendExpressions(expressions []Expression) {
	writer.bytes = append(writer.bytes, '[')
	for index, expression := range expressions {
		if index > 0 {
			writer.bytes = append(writer.bytes, ',')
		}
		writer.appendExpression(expression)
	}
	writer.bytes = append(writer.bytes, ']')
}

func (writer *expressionJSONWriter) appendString(text string) {
	if plainJSONString(text) {
		writer.bytes = append(writer.bytes, '"')
		writer.bytes = append(writer.bytes, text...)
		writer.bytes = append(writer.bytes, '"')
		return
	}
	writer.appendEncodedValue(text)
}

func (writer *expressionJSONWriter) appendValue(value Value) {
	if writer.err != nil {
		return
	}
	writer.bytes = append(writer.bytes, compactValueString(value.raw)...)
}

func (writer *expressionJSONWriter) appendEncodedValue(value any) {
	if writer.err != nil {
		return
	}
	encoded, err := sonic.ConfigStd.Marshal(value)
	writer.err = err
	writer.bytes = append(writer.bytes, encoded...)
}
