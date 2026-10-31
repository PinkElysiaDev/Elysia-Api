package protocol

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const scalarOperandCapacity = 2

func plannedLeaf(value Value) plannedOutput {
	return plannedOutput{JSONLeaf(value), value.Len()}
}

func readPlannedString(output JSONOutput) (string, error) {
	if output.node != nil {
		return "", fmt.Errorf("expected a string")
	}
	return readString(output.value)
}

func readPlannedBoolean(output JSONOutput) (bool, error) {
	if output.node != nil {
		return false, fmt.Errorf("expected a boolean")
	}
	return readBoolean(output.value)
}

func (plan *compiledOutputPlan) buildReadValue(state evaluation) (plannedOutput, error) {
	expression := plan.expression
	root := state.input
	switch expression.from {
	case "root":
		root = state.root
	case "item":
		root = state.item
	case "context":
		root = state.context
	case "runtime":
		if !state.runtime.isBound {
			return plannedOutput{}, fmt.Errorf("runtime metadata is not bound")
		}
		if expression.pointer[0] == "scope" {
			return plannedLeaf(StringValue(state.runtime.scopeModel)), nil
		}
		if expression.pointer[0] == "stream" {
			value, err := state.runtime.readOutputStream(expression.pointer[1])
			if err == nil {
				state.budget.hasReadOutputStream = true
			}
			if err == nil && expression.isRequired && value.output.IsZero() {
				return plannedOutput{}, fmt.Errorf("%s: required field is absent", expression.path)
			}
			return value, err
		}
		return plannedLeaf(coreBoolean(state.runtime.isInterleavedThinking)), nil
	}
	value, err := readValuePointerWithObjectReader(root, expression.pointer, state.valueReader.forParent(root), state.objectReader)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s: %w", expression.path, err)
	}
	if expression.isRequired && value.IsZero() {
		return plannedOutput{}, fmt.Errorf("%s: required field is absent", expression.path)
	}
	return plannedLeaf(value), nil
}

func (plan *compiledOutputPlan) buildScalar(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	expression := plan.expression
	switch expression.op {
	case "split_data_uri":
		output, err := splitDataURI(source.output)
		if err != nil {
			return plannedOutput{}, err
		}
		return plannedOutput{output, normalizedOutputBytes(output)}, nil
	case "exists":
		return plannedLeaf(coreBoolean(!source.output.IsZero())), nil
	case "present":
		return plannedLeaf(coreBoolean(!source.output.IsZero() && !coreOutputIsNull(source.output))), nil
	case "not":
		isTrue, err := readPlannedBoolean(source.output)
		if err != nil {
			return plannedOutput{}, err
		}
		return plannedLeaf(coreBoolean(!isTrue)), nil
	case "cast":
		if source.output.node != nil {
			return plannedOutput{}, fmt.Errorf("unsupported or lossy scalar conversion to %s", expression.to)
		}
		value, err := castValue(source.output.value, expression.to)
		return plannedLeaf(value), err
	case "enum":
		key, err := readPlannedString(source.output)
		if err != nil {
			return plannedOutput{}, err
		}
		value, ok := expression.values[key]
		if !ok {
			return plannedOutput{}, fmt.Errorf("%s/values: enum value is unmapped", expression.path)
		}
		return plannedLeaf(value), nil
	case "strip_prefix", "trim_prefix", "lower", "has_prefix", "contains":
		text, err := readPlannedString(source.output)
		if err != nil {
			return plannedOutput{}, err
		}
		switch expression.op {
		case "lower":
			return plannedLeaf(StringValue(strings.ToLower(text))), nil
		case "has_prefix":
			return plannedLeaf(coreBoolean(strings.HasPrefix(text, expression.literalText))), nil
		case "contains":
			return plannedLeaf(coreBoolean(strings.Contains(text, expression.literalText))), nil
		}
		prefix := expression.literalText
		if expression.op == "strip_prefix" && !strings.HasPrefix(text, prefix) {
			return plannedOutput{}, fmt.Errorf("string does not have the declared prefix")
		}
		return plannedLeaf(StringValue(strings.TrimPrefix(text, prefix))), nil
	}
	panic("uncompiled scalar operation")
}

func (plan *compiledOutputPlan) buildScalarOperands(state evaluation) (plannedOutput, error) {
	var operands []plannedOutput
	var err error
	if len(plan.items) <= scalarOperandCapacity {
		var storage [scalarOperandCapacity]plannedOutput
		operands = storage[:len(plan.items)]
		err = plan.buildOperandValuesInto(state, operands)
	} else {
		operands, err = plan.buildOperandValues(state)
	}
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "equal" {
		return plannedLeaf(coreBoolean(equalPlannedOutputs(operands[0].output, operands[1].output))), nil
	}
	isAll := plan.expression.op == "all"
	result := isAll
	for _, operand := range operands {
		isTrue, err := readPlannedBoolean(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		if isAll {
			result = result && isTrue
		} else {
			result = result || isTrue
		}
	}
	return plannedLeaf(coreBoolean(result)), nil
}

// Compare planned containers without serializing their payloads. Raw leaves
// retain the existing exact-number and decoded-string equality rules.
func equalPlannedOutputs(left, right JSONOutput) bool {
	if left.node == nil && right.node == nil {
		return equalValues(left.value, right.value)
	}
	isObject := left.node != nil && left.node.object || left.node == nil && left.value.IsObject()
	if isObject {
		first, err := plannedObjectFields(left)
		if err != nil {
			return false
		}
		second, err := plannedObjectFields(right)
		if err != nil || len(first) != len(second) {
			return false
		}
		for index, field := range first {
			other := second[index]
			if field.key != other.key || !equalPlannedOutputs(field.value, other.value) {
				return false
			}
		}
		return true
	}
	first, err := plannedArrayItems(left)
	if err != nil {
		return false
	}
	second, err := plannedArrayItems(right)
	if err != nil || len(first) != len(second) {
		return false
	}
	for index, item := range first {
		if !equalPlannedOutputs(item, second[index]) {
			return false
		}
	}
	return true
}

func (plan *compiledOutputPlan) buildJSONScalar(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "stringify_json" {
		if source.output.IsZero() {
			return plannedOutput{}, fmt.Errorf("%s: cannot stringify missing JSON", plan.expression.path)
		}
		// JSON text is the requested result here. Only this selected subtree
		// needs bytes; its expression has already run and is never rerun.
		value, err := source.output.Encode(context.WithoutCancel(state.ctx))
		if err != nil {
			return plannedOutput{}, err
		}
		return plannedLeaf(StringValue(value.raw)), nil
	}
	text, err := readPlannedString(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	value, err := ParseValue([]byte(text))
	if err != nil {
		return plannedOutput{}, err
	}
	if err := checkValueLimits(value, state.budget.limits); err != nil {
		return plannedOutput{}, err
	}
	return plannedLeaf(value), nil
}

func (plan *compiledOutputPlan) buildJoin(state evaluation) (plannedOutput, error) {
	var operands []plannedOutput
	if plan.source != nil {
		source, err := plan.source.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		items, err := plannedArrayItems(source.output)
		if err != nil {
			return plannedOutput{}, err
		}
		operands = make([]plannedOutput, len(items))
		for index, item := range items {
			operands[index] = plannedOutput{item, normalizedOutputBytes(item)}
		}
	} else {
		var err error
		operands, err = plan.buildOperandValues(state)
		if err != nil {
			return plannedOutput{}, err
		}
	}
	var result strings.Builder
	for _, operand := range operands {
		text, err := readPlannedString(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		if result.Len()+len(text) > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, result.Len()+len(text))
		}
		result.WriteString(text)
	}
	return plannedLeaf(StringValue(result.String())), nil
}

func (plan *compiledOutputPlan) buildKeyedArray(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	keys := make([]JSONOutput, len(items))
	for index, item := range items {
		key, err := readOutputPointer(item, plan.expression.pointer, &valueReader{})
		if err != nil {
			return plannedOutput{}, err
		}
		if key.IsZero() {
			return plannedOutput{}, fmt.Errorf("%s/key: key is missing", plan.expression.path)
		}
		keys[index] = key
	}
	if plan.expression.op == "associate" {
		return plan.associateItems(items, keys)
	}
	order := make([]int, len(items))
	for index := range order {
		order[index] = index
	}
	for index := 1; index < len(keys); index++ {
		if _, err := comparePlannedKeys(keys[0], keys[index]); err != nil {
			return plannedOutput{}, fmt.Errorf("%s/key: %w", plan.expression.path, err)
		}
	}
	sort.SliceStable(order, func(left, right int) bool {
		comparison, _ := comparePlannedKeys(keys[order[left]], keys[order[right]])
		return comparison < 0
	})
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	bytes := 2
	for _, index := range order {
		bytes += appendPlannedItem(node, plannedOutput{items[index], normalizedOutputBytes(items[index])})
	}
	return plannedOutput{JSONOutput{node: node}, bytes}, nil
}

func comparePlannedKeys(left, right JSONOutput) (int, error) {
	if left.node != nil || right.node != nil {
		return 0, fmt.Errorf("sort keys must have matching string or number types")
	}
	return compareValues(left.value, right.value)
}

func (plan *compiledOutputPlan) associateItems(items, keys []JSONOutput) (plannedOutput, error) {
	fields := make(map[string]JSONOutput, len(items))
	for index, item := range items {
		key, err := readPlannedString(keys[index])
		if err != nil || key == "" {
			return plannedOutput{}, fmt.Errorf("%s/key: association requires a nonempty string", plan.expression.path)
		}
		if _, exists := fields[key]; exists {
			return plannedOutput{}, fmt.Errorf("%s: duplicate association key", plan.expression.path)
		}
		value := plannedOutput{item, normalizedOutputBytes(item)}.normalizeForContainer()
		fields[key] = value.output
	}
	output := JSONObject(fields)
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
}
