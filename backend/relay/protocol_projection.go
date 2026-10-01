package relay

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// projectProtocolRequest is the temporary typed-wire boundary. The ordered
// protocol request remains authoritative; legacy messages/items are generated
// together for existing vendor encoders and never independently edited.
func projectProtocolRequest(request *protocol.Request, target FormatType) (*MaheshvaraRequest, error) {
	var result MaheshvaraRequest
	if err := decodeProtocolFields(request.Parameters, &result); err != nil {
		return nil, err
	}
	if err := decodeProtocolString(request.Model, &result.Model); err != nil {
		return nil, err
	}
	if !request.ToolChoice.IsZero() {
		if err := request.ToolChoice.Decode(&result.ToolChoice); err != nil {
			return nil, err
		}
	}
	result.Tools = nil
	result.Messages = nil
	result.InputItems = nil
	result.Instructions = ""
	for _, tool := range request.Tools {
		converted, err := projectProtocolTool(tool, target)
		if err != nil {
			return nil, err
		}
		result.Tools = append(result.Tools, converted)
	}
	for _, node := range request.Content {
		if target == FormatResponses {
			break
		}
		message, err := projectProtocolHistory(node, target)
		if err != nil {
			return nil, err
		}
		if message != nil {
			result.Messages = append(result.Messages, *message)
		}
	}
	for _, intent := range request.Cache {
		switch intent.Kind {
		case "breakpoint":
			if err := intent.Value.Decode(&result.CacheControl); err != nil {
				return nil, err
			}
		case "key":
			if err := decodeProtocolString(intent.Value, &result.PromptCacheKey); err != nil {
				return nil, err
			}
		case "retention":
			result.PromptCacheRetention = intent.Value.Bytes()
		case "resource":
			if intent.Resource == nil || target != FormatGemini {
				return nil, fmt.Errorf("unsupported cache resource target")
			}
			var id string
			if err := decodeProtocolString(intent.Resource.ID, &id); err != nil {
				return nil, err
			}
			result.CacheControl = map[string]any{"cachedContent": id}
		default:
			return nil, fmt.Errorf("unsupported cache intent %q", intent.Kind)
		}
	}
	return &result, nil
}

func projectProtocolTool(tool protocol.Tool, target FormatType) (MaheshvaraTool, error) {
	var result MaheshvaraTool
	if err := decodeProtocolFields(tool.Options, &result); err != nil {
		return result, err
	}
	for _, entry := range []struct {
		value  protocol.Value
		target *string
	}{{tool.Name, &result.Name}, {tool.Description, &result.Description}} {
		if err := decodeProtocolString(entry.value, entry.target); err != nil {
			return result, err
		}
	}
	if !tool.InputSchema.IsZero() {
		if err := tool.InputSchema.Decode(&result.Parameters); err != nil {
			return result, err
		}
	}
	if tool.Kind == protocol.FunctionTool {
		result.Type = MaheshvaraToolFunction
	}
	if tool.Kind == protocol.FreeTextTool {
		result.Type = "custom"
	}
	if tool.Kind != protocol.FunctionTool && tool.Native != nil && protocol.CanPreserveNative(tool.Native.Source, toolNativeTarget(target)) {
		if err := tool.Native.Value.Decode(&result.Raw); err != nil {
			return result, err
		}
		result.sourceFormat = target
	}
	if tool.Kind == protocol.FreeTextTool {
		if result.Raw == nil {
			result.Raw = map[string]any{"type": "custom", "name": result.Name}
		}
		if !tool.Format.IsZero() {
			var format any
			if err := tool.Format.Decode(&format); err != nil {
				return result, err
			}
			result.Raw["format"] = format
		}
		result.sourceFormat = FormatResponses
	}
	if err := projectCacheControl(tool.Cache, &result.CacheControl); err != nil {
		return result, err
	}
	return result, nil
}

func projectProtocolHistory(node protocol.Node, target FormatType) (*MaheshvaraMessage, error) {
	if node.Kind == protocol.MessageNode {
		message := &MaheshvaraMessage{}
		if err := decodeProtocolString(node.Role, &message.Role); err != nil {
			return nil, err
		}
		for index, child := range node.Children {
			if child.Kind == protocol.ToolCallNode {
				call, err := projectProtocolCall(child)
				if err != nil {
					return nil, err
				}
				call.claudeIndex = &index
				message.ToolCalls = append(message.ToolCalls, call)
				continue
			}
			part, err := projectProtocolPart(child, target)
			if err != nil {
				return nil, err
			}
			part.claudeIndex = &index
			message.Content = append(message.Content, part)
		}
		if err := projectCacheControl(node.Cache, &message.CacheControl); err != nil {
			return nil, err
		}
		return message, nil
	}
	if node.Kind == protocol.ToolCallNode {
		call, err := projectProtocolCall(node)
		if err != nil {
			return nil, err
		}
		return &MaheshvaraMessage{Role: "assistant", ToolCalls: []MaheshvaraToolCall{call}}, nil
	}
	if node.Kind == protocol.ToolResultNode {
		part, err := projectProtocolPart(node, target)
		if err != nil {
			return nil, err
		}
		return &MaheshvaraMessage{Role: "tool", ToolCallID: part.ToolCallID, Content: []MaheshvaraContentPart{part}}, nil
	}
	return nil, fmt.Errorf("unsupported_history: node %q is not supported by the transitional wire adapter", node.Kind)
}

func projectProtocolCall(node protocol.Node) (MaheshvaraToolCall, error) {
	call := MaheshvaraToolCall{Type: MaheshvaraToolFunction}
	if err := decodeProtocolString(node.CallID, &call.ID); err != nil {
		return call, err
	}
	if err := decodeProtocolString(node.Name, &call.Name); err != nil {
		return call, err
	}
	if node.Input == nil || node.Input.Value.IsZero() {
		return call, fmt.Errorf("tool input is required")
	}
	if node.Input.Kind == protocol.JSONInput {
		call.Arguments = node.Input.Value.Bytes()
	} else {
		return call, fmt.Errorf("free text tool input requires the Responses adapter")
	}
	if err := projectCacheControl(node.Cache, &call.CacheControl); err != nil {
		return call, err
	}
	return call, nil
}

func projectProtocolPart(node protocol.Node, target FormatType) (MaheshvaraContentPart, error) {
	var part MaheshvaraContentPart
	if err := decodeProtocolFields(node.Attributes, &part); err != nil {
		return part, err
	}
	part.Type = string(node.Kind)
	if node.Kind == protocol.DocumentNode {
		part.Type = MaheshvaraContentFile
	}
	if node.Kind == protocol.TextNode || node.Kind == protocol.RefusalNode {
		if err := decodeProtocolString(node.Payload, &part.Text); err != nil {
			return part, err
		}
	}
	if node.Kind == protocol.ToolResultNode {
		part.Type = MaheshvaraContentToolOutput
		if err := decodeProtocolString(node.CallID, &part.ToolCallID); err != nil {
			return part, err
		}
		if err := decodeProtocolString(node.Payload, &part.ToolOutput); err != nil {
			return part, fmt.Errorf("unsupported_tool_result: %w", err)
		}
	}
	for _, resource := range node.Resources {
		var destination *string
		switch resource.Kind {
		case "signature":
			destination = &part.Signature
		case "encrypted_content":
			destination = &part.EncryptedContent
		case "file_id":
			destination = &part.FileID
		default:
			return part, fmt.Errorf("unsupported resource kind %q", resource.Kind)
		}
		if err := decodeProtocolString(resource.ID, destination); err != nil {
			return part, err
		}
	}
	if err := projectCacheControl(node.Cache, &part.CacheControl); err != nil {
		return part, err
	}
	return part, nil
}

func projectCacheControl(intents []protocol.CacheIntent, target *any) error {
	if len(intents) > 1 {
		return fmt.Errorf("wire node supports only one cache breakpoint")
	}
	for _, intent := range intents {
		if intent.Kind != "breakpoint" {
			return fmt.Errorf("non-breakpoint cache policy on a content/tool node")
		}
		if err := intent.Value.Decode(target); err != nil {
			return err
		}
	}
	return nil
}

func decodeProtocolString(value protocol.Value, target *string) error {
	if value.IsZero() {
		return nil
	}
	if value.IsNull() {
		return fmt.Errorf("null cannot be represented by the transitional string field")
	}
	return value.Decode(target)
}

func decodeProtocolFields(fields protocol.Object, target any) error {
	if len(fields) == 0 {
		return nil
	}
	value, err := protocol.EncodeValue(fields)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(value.Bytes()))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
