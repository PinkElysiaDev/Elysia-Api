package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type evaluation struct {
	ctx                        context.Context
	input, item, root, context Value
	budget                     *evaluationBudget
}

type evaluationBudget struct {
	steps  int
	limits Limits
}

func (expression *compiledExpression) evaluate(state evaluation) (value Value, err error) {
	defer func() { err = locateMappingError(expression.path, err) }()
	state.budget.steps++
	if state.budget.steps > state.budget.limits.Nodes {
		return Value{}, fmt.Errorf("%s: evaluation node limit exceeded", expression.path)
	}
	if err := state.ctx.Err(); err != nil {
		return Value{}, err
	}
	defer func() {
		if err == nil && len(value.raw) > state.budget.limits.BufferBytes {
			value = Value{}
			err = fmt.Errorf("%s: output buffer limit exceeded", expression.path)
		}
	}()
	switch expression.op {
	case "omit":
		return Value{}, nil
	case "literal":
		return expression.value, nil
	case "read":
		root := state.input
		switch expression.from {
		case "root":
			root = state.root
		case "item":
			root = state.item
		case "context":
			root = state.context
		}
		value, err := readValuePointer(root, expression.pointer)
		if err != nil {
			return Value{}, fmt.Errorf("%s: %w", expression.path, err)
		}
		if expression.isRequired && value.IsZero() {
			return Value{}, fmt.Errorf("%s: required field is absent", expression.path)
		}
		return value, nil
	case "object":
		object := make(Object, len(expression.fields))
		bytes := 0
		for _, key := range expression.keys {
			value, err := expression.fields[key].evaluate(state)
			if err != nil {
				return Value{}, err
			}
			if !value.IsZero() {
				bytes += len(value.raw) + len(key) + 4
				if bytes > state.budget.limits.BufferBytes {
					return Value{}, fmt.Errorf("%s: object buffer limit exceeded", expression.path)
				}
				object[key] = value
			}
		}
		return EncodeValue(object)
	case "array", "concat", "join", "merge", "equal", "all", "any":
		return expression.evaluateOperands(state)
	case "map", "flatmap", "filter":
		return expression.evaluateArray(state)
	case "if":
		condition, err := expression.when.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		isTrue, err := readBoolean(condition)
		if err != nil {
			return Value{}, fmt.Errorf("%s/when: %w", expression.path, err)
		}
		branch := expression.otherwise
		if isTrue {
			branch = expression.then
		}
		if branch == nil {
			return Value{}, nil
		}
		return branch.evaluate(state)
	case "choose":
		discriminator, err := expression.source.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		key, err := readString(discriminator)
		if err != nil {
			return Value{}, fmt.Errorf("%s/source: %w", expression.path, err)
		}
		branch := expression.cases[key]
		if branch == nil {
			branch = expression.otherwise
		}
		if branch == nil {
			return Value{}, fmt.Errorf("%s/cases: no branch for discriminator", expression.path)
		}
		return branch.evaluate(state)
	}
	source, err := expression.source.evaluate(state)
	if err != nil {
		return Value{}, err
	}
	switch expression.op {
	case "exists":
		return EncodeValue(!source.IsZero())
	case "present":
		// exists 的非空变体：显式 null 视为缺失（兼容上游用 null 占位的字段）。
		return EncodeValue(!source.IsZero() && !source.IsNull())
	case "strip_prefix":
		text, err := readString(source)
		if err != nil {
			return Value{}, err
		}
		prefix, _ := readString(expression.value)
		if !strings.HasPrefix(text, prefix) {
			return Value{}, fmt.Errorf("string does not have the declared prefix")
		}
		return StringValue(strings.TrimPrefix(text, prefix)), nil
	case "trim_prefix":
		// 宽容版 strip_prefix：前缀在则剥除，不在则原样通过——用于「同一字段
		// 在不同兼容上游可能带或不带前缀」的发现/解码场景，转换优先于拒绝。
		text, err := readString(source)
		if err != nil {
			return Value{}, err
		}
		prefix, _ := readString(expression.value)
		return StringValue(strings.TrimPrefix(text, prefix)), nil
	case "not":
		isTrue, err := readBoolean(source)
		if err != nil {
			return Value{}, err
		}
		return EncodeValue(!isTrue)
	case "enum":
		key, err := readString(source)
		if err != nil {
			return Value{}, err
		}
		mapped, exists := expression.values[key]
		if !exists {
			return Value{}, fmt.Errorf("%s/values: enum value is unmapped", expression.path)
		}
		return mapped, nil
	case "cast":
		return castValue(source, expression.to)
	case "parse_json":
		text, err := readString(source)
		if err != nil {
			return Value{}, err
		}
		value, err := ParseValue([]byte(text))
		if err != nil {
			return Value{}, err
		}
		if err := checkValueLimits(value, state.budget.limits); err != nil {
			return Value{}, err
		}
		return value, nil
	case "stringify_json":
		if source.IsZero() {
			return Value{}, fmt.Errorf("%s: cannot stringify missing JSON", expression.path)
		}
		return StringValue(source.raw), nil
	case "sort", "associate":
		return expression.evaluateKeyedArray(source)
	}
	return Value{}, fmt.Errorf("%s: unimplemented compiled operation", expression.path)
}

func (expression *compiledExpression) evaluateArray(state evaluation) (Value, error) {
	source, err := expression.source.evaluate(state)
	if err != nil {
		return Value{}, err
	}
	items, err := readArray(source)
	if err != nil {
		return Value{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	result := make([]Value, 0, len(items))
	bytes := 0
	for _, item := range items {
		child := state
		child.item = item
		if expression.op == "filter" {
			value, err := expression.when.evaluate(child)
			if err != nil {
				return Value{}, err
			}
			isMatch, err := readBoolean(value)
			if err != nil {
				return Value{}, err
			}
			if isMatch {
				result = append(result, item)
			}
			continue
		}
		value, err := expression.body.evaluate(child)
		if err != nil {
			return Value{}, err
		}
		if value.IsZero() {
			continue
		}
		if expression.op == "flatmap" {
			values, err := readArray(value)
			if err != nil {
				return Value{}, err
			}
			result = append(result, values...)
		} else {
			result = append(result, value)
		}
		bytes += len(value.raw) + 1
		if bytes > state.budget.limits.BufferBytes {
			return Value{}, fmt.Errorf("%s: array buffer limit exceeded", expression.path)
		}
		if len(result) > state.budget.limits.Nodes {
			return Value{}, fmt.Errorf("%s: array item limit exceeded", expression.path)
		}
	}
	return EncodeValue(result)
}

func (expression *compiledExpression) evaluateOperands(state evaluation) (Value, error) {
	values := make([]Value, 0, len(expression.items))
	if expression.source != nil {
		// Only join accepts a dynamic operand array; compilation excludes items.
		source, err := expression.source.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		values, err = readArray(source)
		if err != nil {
			return Value{}, err
		}
	}
	bytes := 0
	for _, item := range expression.items {
		value, err := item.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		values = append(values, value)
		bytes += len(value.raw) + 1
		if bytes > state.budget.limits.BufferBytes {
			return Value{}, fmt.Errorf("%s: operand buffer limit exceeded", expression.path)
		}
	}
	switch expression.op {
	case "equal":
		return EncodeValue(equalValues(values[0], values[1]))
	case "all", "any":
		result := expression.op == "all"
		for _, value := range values {
			isTrue, err := readBoolean(value)
			if err != nil {
				return Value{}, err
			}
			if expression.op == "all" {
				result = result && isTrue
			} else {
				result = result || isTrue
			}
		}
		return EncodeValue(result)
	case "array":
		present := make([]Value, 0, len(values))
		for _, value := range values {
			if !value.IsZero() {
				present = append(present, value)
			}
		}
		return EncodeValue(present)
	case "concat":
		result := make([]Value, 0)
		for _, value := range values {
			items, err := readArray(value)
			if err != nil {
				return Value{}, err
			}
			result = append(result, items...)
			if len(result) > state.budget.limits.Nodes {
				return Value{}, fmt.Errorf("%s: array item limit exceeded", expression.path)
			}
		}
		return EncodeValue(result)
	case "join":
		var result strings.Builder
		for _, value := range values {
			text, err := readString(value)
			if err != nil {
				return Value{}, err
			}
			if result.Len()+len(text) > state.budget.limits.BufferBytes {
				return Value{}, fmt.Errorf("%s: string buffer limit exceeded", expression.path)
			}
			result.WriteString(text)
		}
		return StringValue(result.String()), nil
	case "merge":
		result := make(Object)
		for _, value := range values {
			object, err := value.ReadObject()
			if err != nil {
				return Value{}, err
			}
			for _, key := range sortedKeys(object) {
				if _, exists := result[key]; exists {
					switch expression.policy {
					case "reject":
						return Value{}, fmt.Errorf("%s: conflicting merge field %q", expression.path, key)
					case "first":
						continue
					}
				}
				result[key] = object[key]
			}
		}
		return EncodeValue(result)
	}
	return Value{}, fmt.Errorf("%s: unsupported operand operation", expression.path)
}

func (expression *compiledExpression) evaluateKeyedArray(source Value) (Value, error) {
	items, err := readArray(source)
	if err != nil {
		return Value{}, err
	}
	keys := make([]Value, len(items))
	for index, item := range items {
		key, err := readValuePointer(item, expression.pointer)
		if err != nil {
			return Value{}, err
		}
		if key.IsZero() {
			return Value{}, fmt.Errorf("%s/key: key is missing", expression.path)
		}
		keys[index] = key
	}
	if expression.op == "associate" {
		object := make(Object, len(items))
		for index, item := range items {
			key, err := readString(keys[index])
			if err != nil || key == "" {
				return Value{}, fmt.Errorf("%s/key: association requires a nonempty string", expression.path)
			}
			if _, exists := object[key]; exists {
				return Value{}, fmt.Errorf("%s: duplicate association key", expression.path)
			}
			object[key] = item
		}
		return EncodeValue(object)
	}
	order := make([]int, len(items))
	for index := range order {
		order[index] = index
	}
	for index := 1; index < len(keys); index++ {
		if _, err := compareValues(keys[0], keys[index]); err != nil {
			return Value{}, fmt.Errorf("%s/key: %w", expression.path, err)
		}
	}
	sort.SliceStable(order, func(left, right int) bool {
		comparison, _ := compareValues(keys[order[left]], keys[order[right]])
		return comparison < 0
	})
	result := make([]Value, len(items))
	for index, sourceIndex := range order {
		result[index] = items[sourceIndex]
	}
	return EncodeValue(result)
}

func readValuePointer(value Value, pointer []string) (Value, error) {
	for _, key := range pointer {
		if value.IsZero() {
			return Value{}, nil
		}
		if value.IsObject() {
			object, err := value.ReadObject()
			if err != nil {
				return Value{}, err
			}
			value = object[key]
			continue
		}
		if valueType(value) != ArrayType {
			return Value{}, fmt.Errorf("JSON pointer traverses a scalar")
		}
		items, err := readArray(value)
		if err != nil {
			return Value{}, err
		}
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 || strconv.Itoa(index) != key {
			return Value{}, fmt.Errorf("invalid array index in JSON pointer")
		}
		if index >= len(items) {
			return Value{}, nil
		}
		value = items[index]
	}
	return value, nil
}

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
