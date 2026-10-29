package protocol

import (
	"fmt"
	"strconv"
)

func (state evaluation) hasOutputReader(from string) bool {
	switch from {
	case "input":
		return state.inputReader != nil
	case "root":
		return state.rootReader != nil
	case "item":
		return state.itemOutput != nil
	}
	return false
}

// Core readers select only the requested field. Container values stay as output
// plans until a scalar operation or the final wire actually needs JSON bytes.
func (expression *compiledExpression) readOutput(state evaluation) (JSONOutput, bool, error) {
	var output JSONOutput
	var err error
	switch {
	case expression.from == "input" && state.inputReader != nil:
		output, err = state.inputReader(expression.pointer)
	case expression.from == "root" && state.rootReader != nil:
		output, err = state.rootReader(expression.pointer)
	case expression.from == "item" && state.itemOutput != nil:
		output, err = readOutputPointer(*state.itemOutput, expression.pointer, state.valueReader)
	default:
		return JSONOutput{}, false, nil
	}
	if err != nil {
		return JSONOutput{}, true, fmt.Errorf("%s: %w", expression.path, err)
	}
	if expression.isRequired && output.IsZero() {
		return JSONOutput{}, true, fmt.Errorf("%s: required field is absent", expression.path)
	}
	return output, true, nil
}

func (expression *compiledExpression) buildReadOutput(state evaluation) (value plannedOutput, err error) {
	defer func() { err = locateMappingError(expression.path, err) }()
	if err := state.advance(); err != nil {
		return plannedOutput{}, err
	}
	output, _, err := expression.readOutput(state)
	if err != nil || output.IsZero() {
		return plannedOutput{}, err
	}
	bytes := normalizedOutputBytes(output)
	if bytes > state.budget.limits.BufferBytes {
		return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
	}
	return plannedOutput{output, bytes}, nil
}

func readOutputPointer(output JSONOutput, pointer []string, reader *valueReader) (JSONOutput, error) {
	for index, key := range pointer {
		if output.node == nil {
			value, err := readValuePointerWithin(output.value, pointer[index:], reader)
			return JSONLeaf(value), err
		}
		if output.node.object {
			var selected JSONOutput
			for _, field := range output.node.fields {
				if coreJSONKey(field.key) == key {
					selected = field.value
				}
			}
			output = selected
			continue
		}
		item, err := strconv.Atoi(key)
		if err != nil || item < 0 || strconv.Itoa(item) != key {
			return JSONOutput{}, fmt.Errorf("invalid array index in JSON pointer")
		}
		if item >= len(output.node.items) {
			return JSONOutput{}, nil
		}
		output = output.node.items[item]
	}
	return output, nil
}
