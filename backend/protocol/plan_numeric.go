package protocol

import (
	"fmt"
	"math"
)

func (plan *compiledOutputPlan) buildTypeQuery(state evaluation) (plannedOutput, error) {
	if plan.hasRequestObjectSource && state.coreRequest != nil {
		object, isPresent, err := plan.source.expression.readRequestObject(state)
		if err != nil {
			return plannedOutput{}, err
		}
		kind := "object"
		if !isPresent {
			kind = "missing"
		} else if object == nil {
			kind = "null"
		}
		return plannedLeaf(StringValue(kind)), nil
	}
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "type" {
		return plannedLeaf(StringValue(plannedJSONType(source.output))), nil
	}
	if plannedJSONType(source.output) != "number" {
		return plannedLeaf(coreBoolean(false)), nil
	}
	var number int64
	return plannedLeaf(coreBoolean(source.output.value.Decode(&number) == nil)), nil
}

// Values are already valid JSON. The leading token identifies their kind;
// inspecting a numeric type must not expand an exponent or decode a payload.
func plannedJSONType(output JSONOutput) string {
	if output.node != nil {
		if output.node.object {
			return "object"
		}
		return "array"
	}
	return jsonValueKind(output.value)
}

func readPlannedInteger(output JSONOutput) (int64, error) {
	if plannedJSONType(output) != "number" {
		return 0, fmt.Errorf("expected an int64 number")
	}
	var value int64
	if err := output.value.Decode(&value); err != nil {
		return 0, fmt.Errorf("expected an int64 number: %w", err)
	}
	return value, nil
}

func (plan *compiledOutputPlan) buildInteger(state evaluation) (plannedOutput, error) {
	operands, err := plan.buildOperandValues(state)
	if err != nil {
		return plannedOutput{}, err
	}
	values := make([]int64, len(operands))
	for index, operand := range operands {
		values[index], err = readPlannedInteger(operand.output)
		if err != nil {
			return plannedOutput{}, locateMappingError(plan.items[index].expression.path, err)
		}
	}
	left, right := values[0], values[1]
	switch plan.expression.operation {
	case "lt":
		return plannedLeaf(coreBoolean(left < right)), nil
	case "lte":
		return plannedLeaf(coreBoolean(left <= right)), nil
	case "gt":
		return plannedLeaf(coreBoolean(left > right)), nil
	case "gte":
		return plannedLeaf(coreBoolean(left >= right)), nil
	case "subtract":
		if right > 0 && left < math.MinInt64+right || right < 0 && left > math.MaxInt64+right {
			return plannedOutput{}, fmt.Errorf("int64 subtraction overflow")
		}
		return plannedLeaf(coreInteger(left - right)), nil
	case "multiply":
		product := left * right
		if left != 0 && product/left != right || left == -1 && right == math.MinInt64 {
			return plannedOutput{}, fmt.Errorf("int64 multiplication overflow")
		}
		return plannedLeaf(coreInteger(product)), nil
	case "divide", "remainder":
		if right == 0 {
			return plannedOutput{}, fmt.Errorf("int64 division by zero")
		}
		if plan.expression.operation == "remainder" {
			return plannedLeaf(coreInteger(left % right)), nil
		}
		if left == math.MinInt64 && right == -1 {
			return plannedOutput{}, fmt.Errorf("int64 division overflow")
		}
		return plannedLeaf(coreInteger(left / right)), nil
	case "add":
		total := left
		for _, next := range values[1:] {
			if next > 0 && total > math.MaxInt64-next || next < 0 && total < math.MinInt64-next {
				return plannedOutput{}, fmt.Errorf("int64 addition overflow")
			}
			total += next
		}
		return plannedLeaf(coreInteger(total)), nil
	}
	panic("uncompiled integer operation")
}
