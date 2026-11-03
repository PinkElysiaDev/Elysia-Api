package protocol

import "fmt"

func compileErrorControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	var name string
	switch path {
	case "/message", "/code", "/param", "/details", "/providerType":
		name = path[1:]
	default:
		return nil, "", fmt.Errorf("error controls cannot update %s", path)
	}
	kind := AnyType
	if name == "message" {
		kind = StringType
	}
	return func(document any, output JSONOutput, reader *valueReader) error {
		if name == "message" {
			if _, err := readPlannedString(output); err != nil {
				return err
			}
		}
		return setControlObjectField(document.(*Object), name, output, reader)
	}, kind, nil
}

func (each *compiledControlEach) applyErrors(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	hasChanges := false
	visit := func(value *Value, path string) error {
		if value.IsZero() || value.IsNull() {
			return nil
		}
		if err := state.advance(); err != nil {
			return err
		}
		fields, err := value.ReadObject()
		if err != nil {
			return streamIssue(InvalidInput, path, "error controls require an object")
		}
		local := each.local(state, path, func(pointer []string) (JSONOutput, error) { return readCoreObject(fields, pointer, nil) })
		result, err := applyControlSteps(local, conversion, &fields, route, rule, sink, each.steps, owner)
		if err != nil || !result.hasChanges {
			return err
		}
		*value, err = EncodeValue(fields)
		hasChanges = true
		return err
	}
	switch current := document.(type) {
	case *Response:
		err := visit(&current.Error, "/error")
		return hasChanges, err
	case *Event:
		err := visit(&current.Error, "/error")
		if err == nil && current.Response != nil {
			err = visit(&current.Response.Error, "/response/error")
		}
		return hasChanges, err
	default:
		panic("compiled error selector received a different core type")
	}
}
