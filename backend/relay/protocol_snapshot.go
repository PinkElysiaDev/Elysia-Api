package relay

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/elysia-api/backend/protocol"
)

// SnapshotProtocolRequest adapts legacy callers to the ordered contract during
// migration. Responses InputItems are authoritative when present; Messages is
// then only the legacy projection. The returned document has one content list.
// It cannot recover precision already lost by a legacy parser; new wire
// adapters must capture Native directly from the received bytes.
func SnapshotProtocolRequest(req *MaheshvaraRequest, source protocol.Identity, scope protocol.Scope) (*protocol.Request, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot snapshot a nil request")
	}
	fields, err := snapshotObject(req)
	if err != nil {
		return nil, err
	}
	builder := protocolSnapshotBuilder{source: source, scope: scope}
	request := &protocol.Request{SchemaVersion: protocol.SemanticSchemaVersion, Source: source,
		Model: fields["model"], ToolChoice: fields["tool_choice"], Content: []protocol.Node{}, Parameters: fields}
	if system := req.RawExtra["claude_system_blocks"]; len(system) > 0 {
		var blocks []any
		if err := json.Unmarshal(system, &blocks); err != nil {
			return nil, err
		}
		node, err := builder.message(MaheshvaraMessage{Role: "system", Content: interfaceToContentParts(blocks)}, "/system")
		if err != nil {
			return nil, err
		}
		request.Content = append(request.Content, node)
	} else if req.Instructions != "" {
		request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue("system"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(req.Instructions)}}})
	}
	if len(req.InputItems) > 0 {
		for index, item := range req.InputItems {
			node, err := builder.inputItem(item, fmt.Sprintf("/input/%d", index))
			if err != nil {
				return nil, err
			}
			request.Content = append(request.Content, node)
		}
	} else {
		for index, message := range req.Messages {
			node, err := builder.message(message, fmt.Sprintf("/messages/%d", index))
			if err != nil {
				return nil, err
			}
			request.Content = append(request.Content, node)
		}
	}
	for index, tool := range req.Tools {
		converted, err := builder.tool(tool, fmt.Sprintf("/tools/%d", index))
		if err != nil {
			return nil, err
		}
		request.Tools = append(request.Tools, converted)
	}
	for _, entry := range []struct{ field, kind string }{{"cache_control", "breakpoint"}, {"prompt_cache_key", "key"}, {"prompt_cache_retention", "retention"}} {
		value, hasValue := fields[entry.field]
		if !hasValue {
			continue
		}
		intent := protocol.CacheIntent{Kind: entry.kind, Location: "request", Value: value}
		if entry.field == "cache_control" && cachedContentReference(req.CacheControl) != "" {
			intent.Kind = "resource"
			intent.Value = protocol.Value{}
			intent.Resource = &protocol.Resource{Kind: "cache", ID: value, Scope: scope}
		}
		request.Cache = append(request.Cache, intent)
		delete(fields, entry.field)
	}
	for _, key := range []string{"model", "instructions", "messages", "input_items", "tools", "tool_choice"} {
		delete(fields, key)
	}
	return request, nil
}

type protocolSnapshotBuilder struct {
	source protocol.Identity
	scope  protocol.Scope
}

func snapshotObject(value any) (protocol.Object, error) {
	encoded, err := protocol.EncodeValue(value)
	if err != nil {
		return nil, err
	}
	return encoded.ReadObject()
}

func (builder protocolSnapshotBuilder) native(value any, path string) (*protocol.Native, error) {
	encoded, err := protocol.EncodeValue(value)
	if err != nil {
		return nil, err
	}
	return &protocol.Native{Source: protocol.Provenance{Protocol: builder.source, Direction: protocol.DecodeRequest, Path: path, Scope: builder.scope}, Value: encoded}, nil
}

func (builder protocolSnapshotBuilder) message(message MaheshvaraMessage, path string) (protocol.Node, error) {
	node := protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue(message.Role)}
	type orderedNode struct {
		index int
		node  protocol.Node
	}
	var children []orderedNode
	for index, part := range message.Content {
		position := index
		if part.claudeIndex != nil {
			position = *part.claudeIndex
		}
		child, err := builder.part(part, fmt.Sprintf("%s/content/%d", path, position))
		if err != nil {
			return node, err
		}
		children = append(children, orderedNode{position, child})
	}
	for index, call := range message.ToolCalls {
		position := len(message.Content) + index
		if call.claudeIndex != nil {
			position = *call.claudeIndex
		}
		child := protocol.Node{Kind: protocol.ToolCallNode, CallID: protocol.StringValue(call.ID), Name: protocol.StringValue(call.Name)}
		arguments, err := protocol.ParseValue(call.Arguments)
		if err != nil {
			return node, fmt.Errorf("%s tool %d arguments: %w", path, index, err)
		}
		child.Input = &protocol.ToolInput{Kind: protocol.JSONInput, Value: arguments}
		child.Native, err = builder.native(call.Raw, fmt.Sprintf("%s/tool_calls/%d", path, index))
		if err != nil {
			return node, err
		}
		if call.CacheControl != nil {
			value, err := protocol.EncodeValue(call.CacheControl)
			if err != nil {
				return node, err
			}
			child.Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "tool_call", Value: value}}
		}
		children = append(children, orderedNode{position, child})
	}
	sort.SliceStable(children, func(left, right int) bool { return children[left].index < children[right].index })
	for _, child := range children {
		node.Children = append(node.Children, child.node)
	}
	return node, nil
}

func (builder protocolSnapshotBuilder) part(part MaheshvaraContentPart, path string) (protocol.Node, error) {
	fields, err := snapshotObject(part)
	if err != nil {
		return protocol.Node{}, err
	}
	kind := protocol.NodeKind(part.Type)
	switch part.Type {
	case MaheshvaraContentToolOutput:
		kind = protocol.ToolResultNode
	case MaheshvaraContentFile:
		kind = protocol.DocumentNode
	}
	node := protocol.Node{Kind: kind, Attributes: fields}
	if part.Type == MaheshvaraContentText {
		node.Payload = protocol.StringValue(part.Text)
		delete(fields, "text")
	}
	if kind == protocol.ToolResultNode {
		node.CallID = protocol.StringValue(part.ToolCallID)
		node.Payload = protocol.StringValue(part.ToolOutput)
		delete(fields, "tool_call_id")
		delete(fields, "tool_output")
	}
	if part.Raw != nil {
		node.Native, err = builder.native(part.Raw, path)
	}
	if control, hasControl := fields["cache_control"]; hasControl {
		node.Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "content", Value: control}}
		delete(fields, "cache_control")
	}
	for _, key := range []string{"signature", "encrypted_content", "file_id"} {
		if value, hasValue := fields[key]; hasValue {
			node.Resources = append(node.Resources, protocol.Resource{Kind: key, ID: value, Scope: builder.scope})
			delete(fields, key)
		}
	}
	delete(fields, "type")
	delete(fields, "raw")
	return node, err
}

func (builder protocolSnapshotBuilder) inputItem(item MaheshvaraInputItem, path string) (protocol.Node, error) {
	if item.Type == MaheshvaraInputMessage {
		return builder.message(MaheshvaraMessage{Role: item.Role, Content: item.Content}, path)
	}
	raw := rawResponsesInputItem(item.RawExtra)
	fields, err := snapshotObject(raw)
	if err != nil {
		return protocol.Node{}, err
	}
	native, err := builder.native(raw, path)
	if err != nil {
		return protocol.Node{}, err
	}
	node := protocol.Node{Kind: protocol.OpaqueNode, Native: native, ID: fields["id"], CallID: fields["call_id"], Name: fields["name"]}
	switch item.Type {
	case "function_call", "custom_tool_call":
		node.Kind = protocol.ToolCallNode
		input := &protocol.ToolInput{Kind: protocol.TextInput, Value: fields["input"]}
		if item.Type == "function_call" {
			var arguments string
			if err := fields["arguments"].Decode(&arguments); err != nil {
				return node, fmt.Errorf("%s/arguments: %w", path, err)
			}
			input.Kind = protocol.JSONInput
			input.Value, err = protocol.ParseValue([]byte(arguments))
			if err != nil {
				return node, fmt.Errorf("%s/arguments: %w", path, err)
			}
		}
		node.Input = input
	case "function_call_output", "custom_tool_call_output":
		node.Kind = protocol.ToolResultNode
		node.Payload = fields["output"]
	case "reasoning":
		node.Kind = protocol.ReasoningNode
	}
	return node, nil
}

func (builder protocolSnapshotBuilder) tool(tool MaheshvaraTool, path string) (protocol.Tool, error) {
	fields, err := snapshotObject(tool)
	if err != nil {
		return protocol.Tool{}, err
	}
	kind := protocol.OpaqueTool
	if tool.Type == MaheshvaraToolFunction {
		kind = protocol.FunctionTool
	}
	if tool.sourceFormat == FormatResponses && tool.Type == "custom" {
		kind = protocol.FreeTextTool
	}
	native, err := builder.native(tool.Raw, path)
	if err != nil {
		return protocol.Tool{}, err
	}
	converted := protocol.Tool{Kind: kind, Name: fields["name"], Description: fields["description"], InputSchema: fields["parameters"], Options: fields, Native: native}
	if converted.InputSchema.IsZero() {
		converted.InputSchema = fields["input_schema"]
	}
	if kind == protocol.FreeTextTool && tool.Raw["format"] != nil {
		converted.Format, err = protocol.EncodeValue(tool.Raw["format"])
		if err != nil {
			return converted, err
		}
	}
	if control, hasControl := fields["cache_control"]; hasControl {
		converted.Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "tool", Value: control}}
	}
	for _, key := range []string{"name", "description", "parameters", "input_schema", "raw", "cache_control"} {
		delete(fields, key)
	}
	return converted, nil
}

// DecodeProtocolSnapshot is the transitional boundary for legacy wire callers.
func DecodeProtocolSnapshot(body []byte, format FormatType, model string, source protocol.Identity, scope protocol.Scope) (*protocol.Request, error) {
	request, _, err := ConvertRequestToMaheshvara(body, format, model)
	if err != nil {
		return nil, err
	}
	snapshot, err := SnapshotProtocolRequest(request, source, scope)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage = body
	snapshot.Native, err = (protocolSnapshotBuilder{source: source, scope: scope}).native(raw, "")
	return snapshot, err
}
