package protocol

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Eighteen-byte plain JSON numbers fit int64; longer spellings use the codec.
const (
	jsonIntegerBase      = 10
	jsonIntegerBits      = 64
	maxIntegerParseBytes = 18
)

func (plan *compiledOutputPlan) buildTypeQuery(state *evaluation) (plannedOutput, error) {
	if plan.borrowsScalarInput {
		borrowed := *state
		borrowed.valueReader = &scalarInputReader
		state = &borrowed
	}
	if plan.hasRequestObjectSource && state.coreRequest != nil {
		object, isPresent, err := plan.source.expression.readRequestObject(*state)
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
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "type" {
		return plannedLeaf(StringValue(plannedJSONType(source.output))), nil
	}
	if plannedJSONType(source.output) != "number" {
		return plannedLeaf(coreBoolean(false)), nil
	}
	if plan.expression.op == "is_integral" {
		return plannedLeaf(coreBoolean(isIntegralNumber(source.output.value.raw))), nil
	}
	_, err = decodeInteger(source.output.value)
	return plannedLeaf(coreBoolean(err == nil)), nil
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
	value, err := decodeInteger(output.value)
	if err != nil {
		return 0, fmt.Errorf("expected an int64 number: %w", err)
	}
	return value, nil
}

// Callers establish a valid numeric Value. Keep the codec's acceptance and
// error contract for decimal, exponent and out-of-range spellings.
func decodeInteger(value Value) (int64, error) {
	raw := strings.TrimSpace(value.raw)
	if len(raw) <= maxIntegerParseBytes && !strings.ContainsAny(raw, ".eE") {
		number, _ := strconv.ParseInt(raw, jsonIntegerBase, jsonIntegerBits)
		return number, nil
	}
	var number int64
	err := value.Decode(&number)
	return number, err
}

func (plan *compiledOutputPlan) buildInteger(state *evaluation) (plannedOutput, error) {
	var storage [operandWorkspaceCapacity]plannedOutput
	operands := selectOperandStorage(storage[:], len(plan.items))
	err := plan.buildOperandValuesInto(state, operands)
	if err != nil {
		return plannedOutput{}, err
	}
	var integers [operandWorkspaceCapacity]int64
	values := selectOperandStorage(integers[:], len(operands))
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

// The caller has established valid numeric JSON. Compare decimal positions so
// even enormous exponents need neither expanded digits nor floating point.
func isIntegralNumber(raw string) bool {
	raw = strings.TrimSpace(raw)
	mantissa, exponent := raw, 0
	if position := strings.IndexAny(raw, "eE"); position >= 0 {
		mantissa = raw[:position]
		exponent = readBoundedExponent(raw[position+1:], len(mantissa))
	}
	fractionalDigits := 0
	if position := strings.IndexByte(mantissa, '.'); position >= 0 {
		fractionalDigits = len(mantissa) - position - 1
	}
	trailingZeros := 0
	for position := len(mantissa) - 1; position >= 0; position-- {
		digit := mantissa[position]
		if digit == '.' || digit == '-' {
			continue
		}
		if digit != '0' {
			return exponent >= fractionalDigits-trailingZeros
		}
		trailingZeros++
	}
	return true
}

// Only the exponent's relation to mantissa length matters for integrality.
func readBoundedExponent(raw string, limit int) int {
	isNegative := raw[0] == '-'
	if isNegative || raw[0] == '+' {
		raw = raw[1:]
	}
	exponent := 0
	for position := range len(raw) {
		digit := int(raw[position] - '0')
		if exponent > limit/jsonIntegerBase || exponent == limit/jsonIntegerBase && digit > limit%jsonIntegerBase {
			exponent = limit
			break
		}
		exponent = exponent*jsonIntegerBase + digit
	}
	if isNegative {
		return -exponent
	}
	return exponent
}
