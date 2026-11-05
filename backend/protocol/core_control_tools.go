package protocol

import (
	"fmt"
	"strconv"
)

func compileToolControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, "", err
	}
	if len(pointer) != 2 || pointer[0] != "options" {
		return nil, "", fmt.Errorf("tool controls can update only declared options")
	}
	name := pointer[1]
	switch name {
	case "strict", "strictDefault", "eager_input_streaming":
		return func(document any, output JSONOutput, reader *valueReader) error {
			return setControlObjectField(&document.(*Tool).Options, name, output, reader)
		}, AnyType, nil
	default:
		return nil, "", fmt.Errorf("tool controls cannot update option %s", name)
	}
}

func (each *compiledControlEach) applyTools(state evaluation, conversion *CompiledConversion, request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	hasChanges := false
	for index := range request.Tools {
		if err := state.advance(); err != nil {
			return hasChanges, err
		}
		tool := &request.Tools[index]
		local := each.local(state, "/tools/"+strconv.Itoa(index), func(pointer []string) (JSONOutput, error) { return readCoreTool(*tool, pointer, nil) })
		result, err := applyControlSteps(local, conversion, tool, route, rule, sink, each.steps, owner)
		if err != nil {
			return hasChanges, err
		}
		hasChanges = hasChanges || result.hasChanges
	}
	return hasChanges, nil
}
