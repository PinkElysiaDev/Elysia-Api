package protocol

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

const maxNumericConversionDigits = 4096

// Native numbers remain unrestricted JSON lexemes. Numeric operations impose
// a finite decimal bound so a short exponent cannot request gigabytes of math.
func exactNumber(text string) (*big.Rat, bool) {
	if len(text) > maxNumericConversionDigits {
		return nil, false
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(text[index+1:])
		if err != nil || exponent > maxNumericConversionDigits || exponent < -maxNumericConversionDigits {
			return nil, false
		}
	}
	return new(big.Rat).SetString(text)
}

func valueType(value Value) JSONType {
	if value.IsZero() {
		return AnyType
	}
	var parsed any
	if value.Decode(&parsed) != nil {
		return AnyType
	}
	switch typed := parsed.(type) {
	case nil:
		return NullType
	case string:
		return StringType
	case bool:
		return BooleanType
	case []any:
		return ArrayType
	case map[string]any:
		return ObjectType
	case json.Number:
		number, ok := exactNumber(typed.String())
		if ok && number.IsInt() {
			return IntegerType
		}
		return NumberType
	}
	return AnyType
}

func isAssignable(actual, expected JSONType) bool {
	return actual == AnyType || expected == AnyType || actual == expected || (actual == IntegerType && expected == NumberType)
}

func isJSONType(kind JSONType) bool {
	switch kind {
	case AnyType, ObjectType, ArrayType, StringType, NumberType, IntegerType, BooleanType, NullType:
		return true
	}
	return false
}

func validateSchema(schema *ValueSchema, path string, depth int, limits Limits) error {
	if schema == nil {
		return nil
	}
	if depth > limits.Depth {
		return fmt.Errorf("%s exceeds schema depth limit", path)
	}
	if !isJSONType(schema.Type) {
		return fmt.Errorf("%s has unsupported type %q", path, schema.Type)
	}
	if schema.Type != ObjectType && (len(schema.Properties) > 0 || len(schema.Required) > 0 || schema.AllowUnknown) {
		return fmt.Errorf("%s declares object rules for a non-object", path)
	}
	if schema.Type != ArrayType && schema.Items != nil {
		return fmt.Errorf("%s declares items for a non-array", path)
	}
	seen := make(map[string]bool)
	for _, key := range schema.Required {
		if _, ok := schema.Properties[key]; !ok || seen[key] {
			return fmt.Errorf("%s required key %q is unknown or duplicate", path, key)
		}
		seen[key] = true
	}
	for _, key := range sortedKeys(schema.Properties) {
		child := schema.Properties[key]
		if err := validateSchema(&child, path+"/properties/"+key, depth+1, limits); err != nil {
			return err
		}
	}
	if err := validateSchema(schema.Items, path+"/items", depth+1, limits); err != nil {
		return err
	}
	for _, value := range schema.Enum {
		if !isAssignable(valueType(value), schema.Type) {
			return fmt.Errorf("%s enum contains an incompatible value", path)
		}
	}
	return nil
}

func checkValueSchema(value Value, schema *ValueSchema, path string, depth int, limits Limits) error {
	if schema == nil {
		return nil
	}
	if depth > limits.Depth {
		return fmt.Errorf("%s exceeds value depth limit", path)
	}
	if value.IsZero() {
		return fmt.Errorf("%s is missing", path)
	}
	if value.IsNull() && schema.Nullable {
		return nil
	}
	if !isAssignable(valueType(value), schema.Type) {
		return fmt.Errorf("%s requires %s", path, schema.Type)
	}
	if len(schema.Enum) > 0 {
		found := false
		for _, allowed := range schema.Enum {
			if equalValues(value, allowed) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is outside the declared enum", path)
		}
	}
	if schema.Type == ObjectType {
		object, err := value.ReadObject()
		if err != nil {
			return err
		}
		for _, required := range schema.Required {
			if _, ok := object[required]; !ok {
				return fmt.Errorf("%s/%s is required", path, required)
			}
		}
		for _, key := range sortedKeys(object) {
			child, ok := schema.Properties[key]
			if !ok {
				if !schema.AllowUnknown {
					return fmt.Errorf("%s/%s is not declared", path, key)
				}
				continue
			}
			if err := checkValueSchema(object[key], &child, path+"/"+key, depth+1, limits); err != nil {
				return err
			}
		}
	}
	if schema.Type == ArrayType && schema.Items != nil {
		var items []Value
		if err := value.Decode(&items); err != nil {
			return err
		}
		for index, item := range items {
			if err := checkValueSchema(item, schema.Items, fmt.Sprintf("%s/%d", path, index), depth+1, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys[V any](object map[string]V) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalValues(left, right Value) bool {
	if left.IsZero() || right.IsZero() {
		return left.IsZero() && right.IsZero()
	}
	if left.raw == right.raw {
		return true
	}
	var a, b any
	if left.Decode(&a) != nil || right.Decode(&b) != nil {
		return false
	}
	return equalJSON(a, b)
}

func equalJSON(left, right any) bool {
	switch a := left.(type) {
	case json.Number:
		b, ok := right.(json.Number)
		if !ok {
			return false
		}
		x, ok := exactNumber(a.String())
		if !ok {
			return false
		}
		y, ok := exactNumber(b.String())
		return ok && x.Cmp(y) == 0
	case map[string]any:
		b, ok := right.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for key, value := range a {
			other, exists := b[key]
			if !exists || !equalJSON(value, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for index, value := range a {
			if !equalJSON(value, b[index]) {
				return false
			}
		}
		return true
	default:
		return left == right
	}
}
