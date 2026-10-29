package protocol

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Reference evaluator retained from 6e7d3434 for byte, budget and error-order
// differentials. Production builds contain only the compiled output-plan backend.
func (expression *compiledExpression) evaluate(state evaluation) (value Value, err error) {
	defer func() { err = locateMappingError(expression.path, err) }()
	if err := state.advance(); err != nil {
		return Value{}, err
	}
	defer func() {
		if err == nil && len(value.raw) > state.budget.limits.BufferBytes {
			err = resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, len(value.raw))
			value = Value{}
		}
	}()
	return expression.evaluateValue(state)
}

func (expression *compiledExpression) evaluateValue(state evaluation) (Value, error) {
	switch expression.op {
	case "omit":
		return Value{}, nil
	case "literal":
		return expression.value, nil
	case "read":
		if output, handled, err := expression.readOutput(state); handled {
			if err != nil || output.IsZero() {
				return Value{}, err
			}
			return output.Encode(context.WithoutCancel(state.ctx))
		}
		root := state.input
		switch expression.from {
		case "root":
			root = state.root
		case "item":
			root = state.item
		case "context":
			root = state.context
		}
		value, err := readValuePointerWithin(root, expression.pointer, state.valueReader)
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
					return Value{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
				}
				object[key] = value
			}
		}
		return EncodeValue(object)
	case "project":
		source, err := expression.source.evaluate(state)
		if err != nil {
			return Value{}, err
		}
		output, err := expression.projectObjectOracle(JSONLeaf(source))
		if err != nil {
			return Value{}, err
		}
		return output.output.Encode(context.WithoutCancel(state.ctx))
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
	case "keys":
		fields, err := source.ReadObject()
		if err != nil {
			return Value{}, err
		}
		keys := sortedKeys(fields)
		values := make([]Value, 0, len(keys))
		bytes := 2
		for index, key := range keys {
			value := StringValue(key)
			if index != 0 {
				bytes++
			}
			bytes += value.Len()
			if bytes > state.budget.limits.BufferBytes {
				return Value{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
			}
			values = append(values, value)
		}
		return EncodeValue(values)
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

// Freeze projection before selection pushdown: normalize every raw member first.
// Sharing the production selector would hide dropped-field regressions.
func (expression *compiledExpression) projectObjectOracle(source JSONOutput) (plannedOutput, error) {
	fields, err := plannedObjectFields(source)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	isInclude := expression.policy == "include"
	capacity := len(fields)
	if isInclude {
		capacity = min(capacity, len(expression.keys))
	}
	node := &jsonOutputNode{object: true, fields: make([]jsonOutputField, 0, capacity)}
	for _, field := range fields {
		_, isSelected := slices.BinarySearch(expression.keys, field.key)
		if isSelected == isInclude {
			node.fields = append(node.fields, field)
		}
	}
	output := JSONOutput{node: node}
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
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
			return Value{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
		if len(result) > state.budget.limits.Nodes {
			return Value{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, len(result))
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
			return Value{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
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
				return Value{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, len(result))
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
				return Value{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, result.Len()+len(text))
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
