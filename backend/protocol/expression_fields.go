package protocol

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type expressionField struct {
	index               int
	omitEmpty, omitZero bool
}

// Inspect only this expression's fields. Child programs are checked when the
// compiler visits them; serializing them here repeats work at every ancestor.
var expressionFields = func() map[string]expressionField {
	fields := make(map[string]expressionField)
	kind := reflect.TypeFor[Expression]()
	for index := range kind.NumField() {
		field := kind.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		fields[tag[0]] = expressionField{index, slices.Contains(tag[1:], "omitempty"), slices.Contains(tag[1:], "omitzero")}
	}
	return fields
}()

func (field expressionField) hasValue(expression reflect.Value) bool {
	value := expression.Field(field.index)
	return !(field.omitEmpty && isEmptyJSONField(value) || field.omitZero && value.IsZero())
}

func checkExpressionFields(expression Expression, operation OperationInfo, path string) error {
	// The JSON decoder also accepts case-insensitive Go field matches. Retain
	// the authored keys so those aliases cannot bypass the DSL's exact names.
	for key := range expression.present {
		if key != "op" && !slices.Contains(operation.Fields, key) {
			return fmt.Errorf("%s/%s: field is not allowed for %s", path, key, operation.Name)
		}
	}
	value := reflect.ValueOf(expression)
	for key, field := range expressionFields {
		if key != "op" && field.hasValue(value) && !slices.Contains(operation.Fields, key) {
			return fmt.Errorf("%s/%s: field is not allowed for %s", path, key, operation.Name)
		}
	}
	for _, key := range operation.Required {
		if !expressionFields[key].hasValue(value) {
			return fmt.Errorf("%s/%s is required for %s", path, key, operation.Name)
		}
	}
	// Value's zero denotes absence and cannot be a literal enum member. JSON
	// input cannot construct it, but Go-authored expressions use this compiler too.
	for key, member := range expression.Values {
		if member.IsZero() {
			return fmt.Errorf("%s/values/%s: absent JSON value requires omission", path, escapePointer(key))
		}
	}
	return nil
}
