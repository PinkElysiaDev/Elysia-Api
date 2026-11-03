package protocol

import (
	"fmt"
	"strconv"
)

func (state *evaluation) hasOutputReader(from string) bool {
	switch from {
	case "input":
		return state.inputReader != nil
	case "root":
		return state.rootReader != nil
	case "item":
		return state.itemOutput != nil
	case "binding", "state":
		return true
	}
	return false
}

// Core readers select only the requested field. Container values stay as output
// plans until a scalar operation or the final wire actually needs JSON bytes.
func (expression *compiledExpression) readOutput(state evaluation) (JSONOutput, bool, error) {
	var output JSONOutput
	var err error
	switch {
	case state.coreRequest != nil && expression.requestField != 0:
		output, err = readCoreRequestField(state.coreRequest, expression.requestField, expression.pointer[1:], state.cache)

	case expression.from == "input" && state.inputReader != nil:
		output, err = state.inputReader(expression.pointer)
	case expression.from == "root" && state.rootReader != nil:
		output, err = state.rootReader(expression.pointer)
	case expression.from == "item" && state.itemOutput != nil:
		output, err = readOutputPointerWithObjectReader(*state.itemOutput, expression.pointer, state.valueReader, state.objectReader)
	case expression.from == "binding":
		output, err = readOutputPointerWithObjectReader(state.bindings[expression.bindingSlot], expression.pointer, state.valueReader, state.objectReader)
	case expression.from == "state":
		output, err = readOutputPointerWithObjectReader(JSONLeaf(state.streamSlots[expression.stateSlot]), expression.pointer, state.valueReader, state.objectReader)
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

func (expression *compiledExpression) readRequestObject(state evaluation) (object Object, isPresent bool, err error) {
	defer func() { err = locateMappingError(expression.path, err) }()
	if err := state.advance(); err != nil {
		return nil, false, err
	}
	object, isPresent, err = selectCoreRequestPointerObject(state.coreRequest, expression.pointer)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", expression.path, err)
	}
	if expression.isRequired && !isPresent {
		return nil, false, fmt.Errorf("%s: required field is absent", expression.path)
	}
	// coreRequest is bound only after the whole input fits this evaluation's
	// budget. Shape queries do not need a normalized copy of every member value.
	return object, isPresent, nil
}

func readOutputPointer(output JSONOutput, pointer []string, reader *valueReader) (JSONOutput, error) {
	return readOutputPointerWithObjectReader(output, pointer, reader, nil)
}

func readOutputPointerWithObjectReader(output JSONOutput, pointer []string, reader *valueReader, objects *planObjectReader) (JSONOutput, error) {
	for index, key := range pointer {
		if output.node == nil {
			value, err := readValuePointerWithObjectReader(output.value, pointer[index:], reader.forParent(output.value), objects)
			return JSONLeaf(value), err
		}
		if output.node.object {
			var selected JSONOutput
			for _, field := range output.node.fields {
				name := field.key
				if !output.node.hasCanonicalKeys {
					name = coreJSONKey(name)
				}
				if name == key {
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
