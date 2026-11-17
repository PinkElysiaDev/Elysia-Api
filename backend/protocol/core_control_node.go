package protocol

import (
	"fmt"
)

func compileNodeControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	var selectField func(*Node) *Value
	switch path {
	case "/id":
		selectField = func(node *Node) *Value { return &node.ID }
	case "/status":
		selectField = func(node *Node) *Value { return &node.Status }
	case "/payload":
		selectField = func(node *Node) *Value { return &node.Payload }
	default:
		return nil, "", fmt.Errorf("node controls can update only id, status or payload")
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
	return setControlObjectField(&response.Attributes, name, output, reader)
}

func setControlObjectField(target *Object, name string, output JSONOutput, reader *valueReader) error {
	if output.IsZero() {
		delete(*target, name)
		return nil
	}
	var value Value
	if err := assignCoreValue(output, &value, reader); err != nil {
		return err
	}
	if *target == nil {
		*target = Object{}
	}
	(*target)[name] = value
	return nil
}

func (each *compiledControlEach) applyNodes(state evaluation, conversion *CompiledConversion, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink, owner *coreControlOptions) (bool, error) {
	hasChanges := false
	err := visitControlNodesPath(state, document, func(node *Node, path diagnosticPath) error {
		if each.selection == "tool_result" && node.Kind != ToolResultNode {
			return nil
		}
		location := ""
		if each.pathSlot >= 0 {
			location = path.String()
		}
		local := each.local(state, location, func(pointer []string) (JSONOutput, error) { return readCoreNode(*node, pointer, nil) })
		result, err := applyControlSteps(local, conversion, node, route, rule, sink, each.steps, owner)
		hasChanges = hasChanges || result.hasChanges
		return err
	})
	return hasChanges, err
}

func compileToolResultControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	switch path {
	case "/payload":
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlValue(output, &document.(*Node).Payload, reader)
		}, AnyType, nil
	case "/status":
		return func(document any, output JSONOutput, reader *valueReader) error {
			return replaceControlValue(output, &document.(*Node).Status, reader)
		}, AnyType, nil
	case "/children":
		return func(document any, _ JSONOutput, _ *valueReader) error {
			document.(*Node).Children = nil
			return nil
		}, AnyType, nil
	case "/childrenPrefix":
		return func(document any, output JSONOutput, _ *valueReader) error {
			text, err := readPlannedString(output)
			if err != nil {
				return err
			}
			node := document.(*Node)
			node.Children = append([]Node{{Kind: TextNode, Payload: StringValue(text)}}, node.Children...)
			return nil
		}, StringType, nil
	default:
		return nil, "", fmt.Errorf("tool result controls can update only payload, status or content children")
	}
}

func visitControlNodes(state evaluation, document any, visit func(*Node, string) error) error {
	return visitControlNodesPath(state, document, func(node *Node, path diagnosticPath) error { return visit(node, path.String()) })
}

func visitControlNodesPath(state evaluation, document any, visit func(*Node, diagnosticPath) error) error {
	var buffer [256]byte
	root := diagnosticPath(buffer[:0])
	switch current := document.(type) {
	case *Request:
		return visitControlNodeSlicePath(&state, current.Content, root.suffix("/content"), visit)
	case *Response:
		return visitControlNodeSlicePath(&state, current.Content, root.suffix("/content"), visit)
	case *Event:
		if current.Item != nil {
			if err := visitControlNodePath(&state, current.Item, root.suffix("/item"), visit); err != nil {
				return err
			}
		}
		if current.Response != nil {
			if err := visitControlNodeSlicePath(&state, current.Response.Content, root.suffix("/response/content"), visit); err != nil {
				return err
			}
		}
		if current.Request != nil {
			return visitControlNodeSlicePath(&state, current.Request.Content, root.suffix("/request/content"), visit)
		}
		return nil
	default:
		panic("compiled node selector received a different core type")
	}
}

func visitControlNodeSlice(state evaluation, nodes []Node, path string, visit func(*Node, string) error) error {
	var buffer [256]byte
	return visitControlNodeSlicePath(&state, nodes, append(diagnosticPath(buffer[:0]), path...), func(node *Node, path diagnosticPath) error { return visit(node, path.String()) })
}

func visitControlNode(state evaluation, node *Node, path string, visit func(*Node, string) error) error {
	var buffer [256]byte
	return visitControlNodePath(&state, node, append(diagnosticPath(buffer[:0]), path...), func(node *Node, path diagnosticPath) error { return visit(node, path.String()) })
}

func visitControlNodeSlicePath(state *evaluation, nodes []Node, path diagnosticPath, visit func(*Node, diagnosticPath) error) error {
	for index := range nodes {
		if err := visitControlNodePath(state, &nodes[index], path.index(index), visit); err != nil {
			return err
		}
	}
	return nil
}

func visitControlNodePath(state *evaluation, node *Node, path diagnosticPath, visit func(*Node, diagnosticPath) error) error {
	if err := state.advance(); err != nil {
		return err
	}
	if err := visit(node, path); err != nil {
		return err
	}
	if err := visitControlNodeSlicePath(state, node.Children, path.suffix("/children"), visit); err != nil {
		return err
	}
	return visitControlNodeSlicePath(state, node.ReasoningContent, path.suffix("/reasoningContent"), visit)
}
