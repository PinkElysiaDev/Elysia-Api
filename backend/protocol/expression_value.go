package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

func readArray(value Value) ([]Value, error) {
	if valueType(value) != ArrayType {
		return nil, fmt.Errorf("expected an array")
	}
	var items []Value
	err := value.Decode(&items)
	return items, err
}

func readBoolean(value Value) (bool, error) {
	if valueType(value) != BooleanType {
		return false, fmt.Errorf("expected a boolean")
	}
	var result bool
	err := value.Decode(&result)
	return result, err
}

func readString(value Value) (string, error) {
	if valueType(value) != StringType {
		return "", fmt.Errorf("expected a string")
	}
	var result string
	err := value.Decode(&result)
	return result, err
}

func castValue(value Value, target JSONType) (Value, error) {
	kind := valueType(value)
	if value.IsZero() || value.IsNull() {
		return Value{}, fmt.Errorf("cannot cast absent or null scalar")
	}
	if kind == target || (kind == IntegerType && target == NumberType) {
		return value, nil
	}
	if target == StringType && (kind == NumberType || kind == IntegerType || kind == BooleanType) {
		return StringValue(value.raw), nil
	}
	if kind == StringType {
		text, _ := readString(value)
		if target == BooleanType {
			if text == "true" {
				return EncodeValue(true)
			}
			if text == "false" {
				return EncodeValue(false)
			}
			return Value{}, fmt.Errorf("boolean strings must be true or false")
		}
		parsed, err := ParseValue([]byte(text))
		if err != nil {
			return Value{}, err
		}
		actual := valueType(parsed)
		if (target == IntegerType && actual == IntegerType) || (target == NumberType && (actual == IntegerType || actual == NumberType)) {
			return parsed, nil
		}
	}
	return Value{}, fmt.Errorf("unsupported or lossy scalar conversion to %s", target)
}

func compareValues(left, right Value) (int, error) {
	var a, b any
	if err := left.Decode(&a); err != nil {
		return 0, err
	}
	if err := right.Decode(&b); err != nil {
		return 0, err
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		if ok {
			return strings.Compare(x, y), nil
		}
	case json.Number:
		y, ok := b.(json.Number)
		if ok {
			first, firstOK := exactNumber(x.String())
			second, secondOK := exactNumber(y.String())
			if firstOK && secondOK {
				return first.Cmp(second), nil
			}
		}
	}
	return 0, fmt.Errorf("sort keys must have matching string or number types")
}
