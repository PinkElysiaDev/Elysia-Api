package protocol

import (
	"fmt"

	"github.com/bytedance/sonic"
)

// Text is used only during the synchronous comparison. Borrowing unescaped
// strings cannot retain an input in a result or stream state.
var comparisonTextDecoder = sonic.Config{ValidateString: true}.Froze()

func (plan *compiledOutputPlan) compareText(state evaluation) (plannedOutput, error) {
	var operands [2]plannedOutput
	if err := plan.buildOperandValuesInto(state, operands[:]); err != nil {
		return plannedOutput{}, err
	}
	left, err := readTextParts(state, operands[0].output)
	if err != nil {
		return plannedOutput{}, err
	}
	right, err := readTextParts(state, operands[1].output)
	if err != nil {
		return plannedOutput{}, err
	}
	return plannedLeaf(coreBoolean(equalTextParts(left, right))), nil
}

func readTextParts(state evaluation, output JSONOutput) ([]string, error) {
	if plannedJSONType(output) == "string" {
		text, err := readComparisonText(output)
		if err != nil {
			return nil, err
		}
		if len(text) > state.budget.limits.BufferBytes {
			return nil, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, len(text))
		}
		return []string{text}, nil
	}
	items, err := plannedArrayItems(output)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(items))
	bytes := 0
	for index, item := range items {
		if err := state.advance(); err != nil {
			return nil, err
		}
		text, err := readComparisonText(item)
		if err != nil {
			return nil, err
		}
		bytes += len(text)
		if bytes > state.budget.limits.BufferBytes {
			return nil, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
		parts[index] = text
	}
	return parts, nil
}

func readComparisonText(output JSONOutput) (string, error) {
	if output.node != nil || !output.value.IsStringValue() {
		return "", fmt.Errorf("expected a string")
	}
	var text string
	err := comparisonTextDecoder.UnmarshalFromString(output.value.raw, &text)
	return text, err
}

// Chunk boundaries carry no text semantics. Each slice is consumed in place;
// unequal prefixes cannot hide an invalid later member because parsing is done.
func equalTextParts(left, right []string) bool {
	var first, second string
	for {
		for first == "" && len(left) != 0 {
			first, left = left[0], left[1:]
		}
		for second == "" && len(right) != 0 {
			second, right = right[0], right[1:]
		}
		if first == "" || second == "" {
			return first == second
		}
		length := min(len(first), len(second))
		if first[:length] != second[:length] {
			return false
		}
		first, second = first[length:], second[length:]
	}
}
