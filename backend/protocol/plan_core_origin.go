package protocol

import (
	"reflect"
	"strconv"
	"strings"
)

func packageCoreInputType(direction Direction) reflect.Type {
	switch direction {
	case EncodeRequest:
		return reflect.TypeFor[Request]()
	case EncodeResponse:
		return reflect.TypeFor[Response]()
	case EncodeEvent, EncodeUpstreamEvent:
		return reflect.TypeFor[Event]()
	}
	return nil
}

// Types here certify reads from the original core, not JSON shape inference.
// Opaque payloads stop traversal even when they contain fields named native.
func coreTypeAtPointer(root reflect.Type, pointer []string) reflect.Type {
	root = coreValueType(root)
	for _, key := range pointer {
		if root == nil {
			return nil
		}
		switch root.Kind() {
		case reflect.Struct:
			var selected reflect.Type
			for index := range root.NumField() {
				field := root.Field(index)
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if field.IsExported() && name != "-" && name == key {
					selected = field.Type
					break
				}
			}
			root = selected
		case reflect.Slice:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || strconv.Itoa(index) != key {
				return nil
			}
			root = root.Elem()
		default:
			return nil
		}
		root = coreValueType(root)
	}
	return root
}

func coreValueType(value reflect.Type) reflect.Type {
	for value != nil && value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value == reflect.TypeFor[Value]() || value != nil && value.Kind() == reflect.Map {
		return nil
	}
	return value
}

func coreArrayElement(value reflect.Type) reflect.Type {
	if value == nil || value.Kind() != reflect.Slice {
		return nil
	}
	return coreValueType(value.Elem())
}

func (expression *compiledExpression) inheritCoreType() {
	switch expression.op {
	case "let", "remember", "with_index":
		expression.coreType = expression.body.coreType
	case "filter", "sort":
		if coreArrayElement(expression.source.coreType) != nil {
			expression.coreType = expression.source.coreType
		}
	case "map":
		if expression.body.coreType != nil {
			expression.coreType = reflect.SliceOf(expression.body.coreType)
		}
	case "flatmap":
		if coreArrayElement(expression.body.coreType) != nil {
			expression.coreType = expression.body.coreType
		}
	case "if":
		expression.coreType = coreBranchType(expression.then, expression.otherwise)
	}
}

func coreBranchType(first, second *compiledExpression) reflect.Type {
	if first == nil || second == nil {
		return nil
	}
	if first.coreType == second.coreType {
		return first.coreType
	}
	// An empty alternative cannot introduce a constructed array member.
	for _, branch := range [][2]*compiledExpression{{first, second}, {second, first}} {
		original, empty := branch[0], branch[1]
		if coreArrayElement(original.coreType) != nil && empty.op == "array" && len(empty.items) == 0 {
			return original.coreType
		}
	}
	return nil
}
