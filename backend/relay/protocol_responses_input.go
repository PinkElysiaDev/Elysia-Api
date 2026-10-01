package relay

import (
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// renderProtocolResponsesInput walks the authoritative history once. It never
// relies on the old Messages/InputItems precedence or relocates system messages.
func renderProtocolResponsesInput(nodes []protocol.Node) ([]protocol.Object, error) {
	builder := responsesInputBuilder{calls: make(map[string]protocol.InputKind)}
	for _, node := range nodes {
		if err := builder.appendNode(node); err != nil {
			return nil, err
		}
	}
	if builder.items == nil {
		return []protocol.Object{}, nil
	}
	return builder.items, nil
}

type responsesInputBuilder struct {
	items []protocol.Object
	calls map[string]protocol.InputKind
}

func (builder *responsesInputBuilder) appendNode(node protocol.Node) error {
	if node.Kind == protocol.MessageNode {
		return builder.appendMessage(node)
	}
	fields := protocol.Object{}
	if !node.ID.IsZero() {
		fields["id"] = node.ID
	}
	if !node.Status.IsZero() {
		fields["status"] = node.Status
	}
	switch node.Kind {
	case protocol.ToolCallNode:
		if node.Input == nil {
			return fmt.Errorf("tool call input is missing")
		}
		fields["call_id"], fields["name"] = node.CallID, node.Name
		var callID string
		if err := node.CallID.Decode(&callID); err != nil {
			return err
		}
		builder.calls[callID] = node.Input.Kind
		if node.Input.Kind == protocol.TextInput {
			fields["type"], fields["input"] = protocol.StringValue("custom_tool_call"), node.Input.Value
		} else {
			fields["type"], fields["arguments"] = protocol.StringValue("function_call"), protocol.StringValue(string(node.Input.Value.Bytes()))
		}
	case protocol.ToolResultNode:
		var callID string
		if err := node.CallID.Decode(&callID); err != nil {
			return err
		}
		typeName := "function_call_output"
		if builder.calls[callID] == protocol.TextInput {
			typeName = "custom_tool_call_output"
		}
		fields["type"], fields["call_id"], fields["output"] = protocol.StringValue(typeName), node.CallID, node.Payload
	case protocol.OpaqueNode, protocol.ReasoningNode:
		if node.Native == nil || !protocol.CanPreserveNative(node.Native.Source, toolNativeTarget(FormatResponses)) {
			return fmt.Errorf("unsupported_native: Responses history item has no compatible provenance")
		}
		native, err := node.Native.Value.ReadObject()
		if err != nil {
			return err
		}
		for key, value := range fields {
			native[key] = value
		}
		fields = native
	default:
		return fmt.Errorf("unsupported Responses history kind %q", node.Kind)
	}
	if len(node.Cache) > 0 {
		return fmt.Errorf("Responses call/result items do not support cache breakpoints")
	}
	builder.items = append(builder.items, fields)
	return nil
}

func (builder *responsesInputBuilder) appendMessage(node protocol.Node) error {
	var role string
	if err := decodeProtocolString(node.Role, &role); err != nil {
		return err
	}
	var content []map[string]any
	flush := func() error {
		if len(content) == 0 {
			return nil
		}
		value, err := protocol.EncodeValue(content)
		if err != nil {
			return err
		}
		builder.items = append(builder.items, protocol.Object{"type": protocol.StringValue("message"), "role": node.Role, "content": value})
		content = nil
		return nil
	}
	for _, child := range node.Children {
		if child.Kind == protocol.ToolCallNode || child.Kind == protocol.ToolResultNode || child.Kind == protocol.OpaqueNode {
			if err := flush(); err != nil {
				return err
			}
			if err := builder.appendNode(child); err != nil {
				return err
			}
			continue
		}
		part, err := projectProtocolPart(child, FormatResponses)
		if err != nil {
			return err
		}
		converted := maheshvaraContentToResponsesInputContent(role, []MaheshvaraContentPart{part})
		if len(converted) == 0 {
			return fmt.Errorf("unsupported content kind %q for Responses", child.Kind)
		}
		content = append(content, converted...)
	}
	if len(node.Children) == 0 {
		empty, err := protocol.EncodeValue([]any{})
		if err != nil {
			return err
		}
		builder.items = append(builder.items, protocol.Object{"type": protocol.StringValue("message"), "role": node.Role, "content": empty})
	}
	return flush()
}
