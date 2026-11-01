package protocol

import (
	"fmt"
	"strconv"
)

func compileNodeControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	var selectField func(*Node) *Value
	switch path {
	case "/id":
		selectField = func(node *Node) *Value { return &node.ID }
	case "/status":
		selectField = func(node *Node) *Value { return &node.Status }
	default:
		return nil, "", fmt.Errorf("node controls can update only id or status")
	}
	return func(document any, output JSONOutput, reader *valueReader) error {
		return replaceControlValue(output, selectField(document.(*Node)), reader)
	}, AnyType, nil
}

func compileResponseAttributeSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, "", err
	}
	if len(pointer) != 2 || pointer[0] != "attributes" {
		return nil, "", fmt.Errorf("response controls can update id, model, usage or one attribute")
	}
	name := pointer[1]
	return func(document any, output JSONOutput, reader *valueReader) error {
		return setResponseAttribute(document.(*Response), name, output, reader)
	}, AnyType, nil
}

func compileEventResponseAttributeSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, "", err
	}
	if len(pointer) != 3 || pointer[0] != "response" || pointer[1] != "attributes" {
		return nil, "", fmt.Errorf("event controls can update usage or one response attribute")
	}
	name := pointer[2]
	return func(document any, output JSONOutput, reader *valueReader) error {
		response := document.(*Event).Response
		if response == nil {
			return &declaredRejectionError{diagnostic: RejectDiagnostic{Code: InvalidInput, Path: "/response"}, reason: "attribute update requires an existing response"}
		}
		return setResponseAttribute(response, name, output, reader)
	}, AnyType, nil
}

func setResponseAttribute(response *Response, name string, output JSONOutput, reader *valueReader) error {
	if output.IsZero() {
		delete(response.Attributes, name)
		return nil
	}
	var value Value
	if err := assignCoreValue(output, &value, reader); err != nil {
		return err
	}
	if response.Attributes == nil {
		response.Attributes = Object{}
	}
	response.Attributes[name] = value
	return nil
}

func (each *compiledControlEach) applyNodes(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	hasChanges := false
	err := visitControlNodes(state, document, func(node *Node, path string) error {
		local := each.local(state, path, func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer) })
		result, err := applyControlSteps(local, conversion, node, route, rule, sink, each.steps, owner)
		hasChanges = hasChanges || result.hasChanges
		return err
	})
	return hasChanges, err
}

func visitControlNodes(state evaluation, document any, visit func(*Node, string) error) error {
	switch current := document.(type) {
	case *Request:
		return visitControlNodeSlice(state, current.Content, "/content", visit)
	case *Response:
		return visitControlNodeSlice(state, current.Content, "/content", visit)
	case *Event:
		if current.Item != nil {
			if err := visitControlNode(state, current.Item, "/item", visit); err != nil {
				return err
			}
		}
		if current.Response != nil {
			if err := visitControlNodeSlice(state, current.Response.Content, "/response/content", visit); err != nil {
				return err
			}
		}
		if current.Request != nil {
			return visitControlNodeSlice(state, current.Request.Content, "/request/content", visit)
		}
		return nil
	default:
		panic("compiled node selector received a different core type")
	}
}

func visitControlNodeSlice(state evaluation, nodes []Node, path string, visit func(*Node, string) error) error {
	for index := range nodes {
		if err := visitControlNode(state, &nodes[index], path+"/"+strconv.Itoa(index), visit); err != nil {
			return err
		}
	}
	return nil
}

func visitControlNode(state evaluation, node *Node, path string, visit func(*Node, string) error) error {
	if err := state.advance(); err != nil {
		return err
	}
	if err := visit(node, path); err != nil {
		return err
	}
	if err := visitControlNodeSlice(state, node.Children, path+"/children", visit); err != nil {
		return err
	}
	return visitControlNodeSlice(state, node.ReasoningContent, path+"/reasoningContent", visit)
}
