package relay

import (
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

func snapshotProtocolResponse(response *MaheshvaraResponse, source protocol.Identity, scope protocol.Scope) (*protocol.Response, error) {
	fields, err := snapshotObject(response)
	if err != nil {
		return nil, err
	}
	result := &protocol.Response{SchemaVersion: protocol.SemanticSchemaVersion, Source: source, ID: fields["id"], Model: fields["model"], Status: fields["status"], Error: fields["error"], Attributes: fields, Content: []protocol.Node{}, Usage: semanticUsage(response.Usage)}
	for _, key := range []string{"id", "model", "status", "error", "output", "usage"} {
		delete(fields, key)
	}
	builder := protocolSnapshotBuilder{source: source, scope: scope, direction: protocol.DecodeResponse}
	for index, item := range response.Output {
		node, err := builder.outputItem(item, fmt.Sprintf("/output/%d", index))
		if err != nil {
			return nil, err
		}
		result.Content = append(result.Content, node)
	}
	return result, nil
}

func (builder protocolSnapshotBuilder) outputItem(item MaheshvaraOutputItem, path string) (protocol.Node, error) {
	if item.Type == MaheshvaraOutputMessage {
		return builder.message(MaheshvaraMessage{Role: item.Role, Content: item.Content, ToolCalls: item.ToolCalls}, path)
	}
	native, err := builder.native(item.Raw, path)
	if err != nil {
		return protocol.Node{}, err
	}
	node := protocol.Node{Kind: protocol.OpaqueNode, Native: native}
	if item.ID != "" {
		node.ID = protocol.StringValue(item.ID)
	}
	if item.Status != "" {
		node.Status = protocol.StringValue(item.Status)
	}
	switch item.Type {
	case MaheshvaraOutputFunctionCall, "custom_tool_call":
		node.Kind, node.CallID, node.Name = protocol.ToolCallNode, protocol.StringValue(item.CallID), protocol.StringValue(item.Name)
		if item.Type == "custom_tool_call" {
			node.Input = &protocol.ToolInput{Kind: protocol.TextInput, Value: protocol.StringValue(item.Input)}
		} else {
			value, err := protocol.ParseValue(item.Arguments)
			if err != nil {
				return node, err
			}
			node.Input = &protocol.ToolInput{Kind: protocol.JSONInput, Value: value}
		}
	case MaheshvaraOutputReasoning:
		node.Kind = protocol.ReasoningNode
		attributes, err := snapshotObject(item)
		if err != nil {
			return node, err
		}
		for _, key := range []string{"type", "id", "status", "raw"} {
			delete(attributes, key)
		}
		node.Attributes = attributes
	}
	return node, nil
}

func projectProtocolResponse(response *protocol.Response, format FormatType) (*MaheshvaraResponse, error) {
	result := &MaheshvaraResponse{}
	if err := decodeProtocolFields(response.Attributes, result); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		value  protocol.Value
		target *string
	}{
		{response.ID, &result.ID}, {response.Model, &result.Model}, {response.Status, &result.Status},
	} {
		if err := decodeProtocolString(field.value, field.target); err != nil {
			return nil, err
		}
	}
	if !response.Error.IsZero() {
		if err := response.Error.Decode(&result.Error); err != nil {
			return nil, err
		}
	}
	if response.Usage != nil {
		result.Usage = &MaheshvaraUsage{}
		known := usageCounters(result.Usage)
		for name := range response.Usage.Details {
			if known[name] == nil {
				return nil, fmt.Errorf("unsupported usage detail %q", name)
			}
		}
		applySemanticUsage(result.Usage, response.Usage)
	}
	result.Output = nil
	for _, node := range response.Content {
		item, err := projectProtocolOutput(node, format)
		if err != nil {
			return nil, err
		}
		result.Output = append(result.Output, item)
	}
	return result, nil
}

func projectProtocolOutput(node protocol.Node, format FormatType) (MaheshvaraOutputItem, error) {
	item := MaheshvaraOutputItem{}
	for _, field := range []struct {
		value  protocol.Value
		target *string
	}{{node.ID, &item.ID}, {node.Status, &item.Status}} {
		if err := decodeProtocolString(field.value, field.target); err != nil {
			return item, err
		}
	}
	switch node.Kind {
	case protocol.MessageNode:
		message, err := projectProtocolHistory(node, format)
		if err != nil {
			return item, err
		}
		item.Type, item.Role, item.Content, item.ToolCalls = MaheshvaraOutputMessage, message.Role, message.Content, message.ToolCalls
	case protocol.ToolCallNode:
		if err := decodeProtocolString(node.CallID, &item.CallID); err != nil {
			return item, err
		}
		if err := decodeProtocolString(node.Name, &item.Name); err != nil {
			return item, err
		}
		if node.Input == nil {
			return item, fmt.Errorf("tool call input is missing")
		}
		if node.Input.Kind == protocol.TextInput {
			item.Type = "custom_tool_call"
			if err := decodeProtocolString(node.Input.Value, &item.Input); err != nil {
				return item, err
			}
		} else {
			item.Type, item.Arguments = MaheshvaraOutputFunctionCall, node.Input.Value.Bytes()
		}
	case protocol.ReasoningNode:
		if err := decodeProtocolFields(node.Attributes, &item); err != nil {
			return item, err
		}
		item.Type = MaheshvaraOutputReasoning
	case protocol.OpaqueNode:
		target := toolNativeTarget(format)
		target.Direction = protocol.EncodeResponse
		if node.Native == nil || !protocol.CanPreserveNative(node.Native.Source, target) {
			return item, fmt.Errorf("unsupported native response item")
		}
		if err := node.Native.Value.Decode(&item.Raw); err != nil {
			return item, err
		}
		item.Type = stringValue(item.Raw["type"])
		item.sourceFormat = format
	default:
		return item, fmt.Errorf("unsupported response node %q", node.Kind)
	}
	return item, nil
}
