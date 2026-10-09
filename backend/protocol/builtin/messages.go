package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) decodeMessages(value p.Value, path string, direction p.Direction, options p.EvaluationContext, history *historyState) ([]p.Node, error) {
	items, err := readArray(value)
	if err != nil {
		return nil, err
	}
	nodes := make([]p.Node, 0, len(items))
	for index, item := range items {
		location := fmt.Sprintf("%s/%d", path, index)
		fields, err := item.ReadObject()
		if err != nil {
			return nil, err
		}
		kind, err := optionalString(fields["type"])
		if err != nil {
			return nil, err
		}
		if adapter.name == Responses && kind != "" && kind != "message" {
			node, err := adapter.decodeResponseItem(item, location, direction, options, history)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
			continue
		}
		roleValue := fields["role"]
		// Gemini permits an omitted role for a single user content item.
		// Keep the native snapshot unchanged so same-wire replay retains absence.
		if adapter.name == Gemini && roleValue.IsZero() {
			roleValue = p.StringValue("user")
			if direction == p.DecodeResponse {
				roleValue = p.StringValue("model")
			}
		}
		role, err := stringValue(roleValue)
		if err != nil {
			return nil, err
		}
		content := fields["content"]
		known := []string{"type", "role", "content", "id", "status"}
		if adapter.name == Gemini {
			content = fields["parts"]
			known = []string{"role", "parts"}
			if role == "model" {
				role = "assistant"
			}
		}
		node := p.Node{Kind: p.MessageNode, Role: p.StringValue(role), ID: fields["id"], Status: fields["status"], Native: adapter.native(item, location, direction, options)}
		if adapter.name == Chat && role == "tool" {
			node.Kind, node.Role, node.CallID, node.Name, node.Payload = p.ToolResultNode, p.Value{}, fields["tool_call_id"], fields["name"], content
			known = append(known, "tool_call_id", "name")
		} else {
			node.Children, err = adapter.decodeContent(content, location+"/content", direction, options, history)
			if err != nil {
				return nil, err
			}
		}
		if adapter.name == Chat {
			calls, err := readToolCalls(fields["tool_calls"])
			if err != nil {
				return nil, err
			}
			for callIndex, callValue := range calls {
				call, err := callValue.ReadObject()
				if err != nil {
					return nil, err
				}
				callKind, err := stringValue(call["type"])
				if err != nil {
					return nil, err
				}
				if callKind != "function" {
					return nil, unsupported(location+"/tool_calls", "Chat tool history requires function calls")
				}
				function, err := call["function"].ReadObject()
				if err != nil {
					return nil, err
				}
				arguments, err := readJSONArguments(function["arguments"])
				if err != nil {
					return nil, err
				}
				attributes, err := adapter.nestedExtensions(call, []string{"type", "id", "function"}, map[string][]string{"function": {"name", "arguments"}})
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, p.Node{Kind: p.ToolCallNode, Name: function["name"], CallID: call["id"], Input: &p.ToolInput{Kind: p.JSONInput, Value: arguments}, Native: adapter.native(callValue, fmt.Sprintf("%s/tool_calls/%d", location, callIndex), direction, options), Attributes: attributes})
			}
			known = append(known, "tool_calls", "reasoning_content", "refusal")
			if reasoning := fields["reasoning_content"]; !reasoning.IsZero() {
				node.Children = append([]p.Node{{Kind: p.ReasoningNode, Payload: reasoning}}, node.Children...)
			}
			if refusal := fields["refusal"]; !refusal.IsZero() {
				node.Children = append(node.Children, p.Node{Kind: p.RefusalNode, Payload: refusal})
			}
		}
		node.Metadata, err = adapter.extractMetadata(fields, "message", location)
		if err != nil {
			return nil, err
		}
		node.Attributes = adapter.extensions(fields, known)
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// A nullable Chat tool list denotes no calls. The native snapshot retains its
// presence so an unchanged or unrelated edit cannot turn null into omission.
func readToolCalls(value p.Value) ([]p.Value, error) {
	if value.IsNull() {
		return nil, nil
	}
	return readArray(value)
}

func (adapter module) decodeResponseItem(value p.Value, path string, direction p.Direction, options p.EvaluationContext, history *historyState) (p.Node, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return p.Node{}, err
	}
	if kind, err := optionalString(fields["type"]); err != nil {
		return p.Node{}, err
	} else if kind == "reasoning" {
		node := p.Node{Kind: p.ReasoningNode, ReasoningForm: "summary", ID: fields["id"], Status: fields["status"], Native: adapter.native(value, path, direction, options)}
		if encrypted := fields["encrypted_content"]; !encrypted.IsZero() {
			node.Resources = append(node.Resources, p.Resource{Kind: "encrypted_content", ID: encrypted, Scope: options.Scope})
		}
		node.Attributes = adapter.extensions(fields, []string{"type", "id", "status", "encrypted_content", "summary"})
		summaries, err := readArray(fields["summary"])
		if err != nil {
			return node, err
		}
		for _, summary := range summaries {
			entry, err := summary.ReadObject()
			if err != nil {
				return node, err
			}
			node.Children = append(node.Children, p.Node{Kind: p.TextNode, Payload: entry["text"], Attributes: adapter.extensions(entry, []string{"type", "text"})})
		}
		return node, nil
	}
	return adapter.decodeBlock(value, path, direction, options, history)
}

func (adapter module) encodeMessages(nodes []p.Node, direction p.Direction, options p.EvaluationContext) (p.Value, p.Value, error) {
	var messages, system []p.Value
	hasConversation := false
	for index, node := range nodes {
		if node.Kind != p.MessageNode {
			if adapter.name == Responses {
				item, err := adapter.encodeBlock(node, direction, options)
				if err != nil {
					return p.Value{}, p.Value{}, err
				}
				messages = append(messages, item)
				continue
			}
			role := "assistant"
			if node.Kind == p.ToolResultNode {
				role = "user"
			}
			node = p.Node{Kind: p.MessageNode, Role: p.StringValue(role), Children: []p.Node{node}}
		}
		if adapter.name != Chat && !node.Attributes["choiceExtensions"].IsZero() {
			return p.Value{}, p.Value{}, unsupported("/choices", "unknown choice extensions require an explicit mapping")
		}
		role, err := stringValue(node.Role)
		if err != nil {
			return p.Value{}, p.Value{}, err
		}
		if (role == "system" || role == "developer") && (adapter.name == Anthropic || adapter.name == Gemini) {
			annotated := !node.ID.IsZero() || !node.Status.IsZero() || len(node.Attributes) > 0 || len(node.Cache) > 0 || len(node.Resources) > 0
			if hasConversation || annotated || role == "developer" {
				return p.Value{}, p.Value{}, unsupported(fmt.Sprintf("/content/%d", index), "target requires top-level system instructions; enable system_instruction_hoist or provide an explicit mapping")
			}
			for _, child := range node.Children {
				block, err := adapter.encodeBlock(child, direction, options)
				if err != nil {
					return p.Value{}, p.Value{}, err
				}
				system = append(system, block)
			}
			continue
		}
		hasConversation = true
		if (adapter.name == Anthropic || adapter.name == Gemini || adapter.name == Chat) && (!node.ID.IsZero() || !node.Status.IsZero()) {
			// Responses 消息 item 的 id/status 是传输记账而非会话语义：跨族
			// 目标没有等价字段，同族经原生回放保留；这里丢弃并给出显式
			// warning（原为硬拒，Codex 等客户端的每条消息都携带 id）。
			warnDropped(options, p.EncodeRequest, fmt.Sprintf("/content/%d", index),
				"message item identity or status dropped: target has no per-message item id",
				"Same-family forwarding preserves item ids through native replay.")
		}
		if adapter.name == Chat {
			entries, err := adapter.encodeChatMessage(node, direction, options)
			if err != nil {
				return p.Value{}, p.Value{}, err
			}
			messages = append(messages, entries...)
			continue
		}
		fields := p.Object{"role": node.Role, "id": node.ID, "status": node.Status}
		if err := adapter.preserveExtensions(fields, node.Attributes); err != nil {
			return p.Value{}, p.Value{}, err
		}
		var parts []p.Value
		flush := func() {
			if len(parts) > 0 {
				copy := copyFields(fields)
				copy["type"] = p.StringValue("message")
				copy["content"] = array(parts)
				messages = append(messages, object(copy))
				parts = nil
			}
		}
		for _, child := range node.Children {
			block, err := adapter.encodeBlock(child, direction, options)
			if err != nil {
				return p.Value{}, p.Value{}, err
			}
			if adapter.name == Responses && (child.Kind == p.ToolCallNode || child.Kind == p.ToolResultNode || child.Kind == p.ReasoningNode || child.Kind == p.OpaqueNode) {
				flush()
				messages = append(messages, block)
			} else {
				parts = append(parts, block)
			}
		}
		switch adapter.name {
		case Gemini:
			if role == "assistant" {
				fields["role"] = p.StringValue("model")
			}
			delete(fields, "id")
			delete(fields, "status")
			fields["parts"] = array(parts)
			messages = append(messages, object(fields))
		case Anthropic:
			delete(fields, "id")
			delete(fields, "status")
			fields["content"] = array(parts)
			messages = append(messages, object(fields))
		case Responses:
			flush()
		}
	}
	var systemValue p.Value
	if system != nil {
		systemValue = array(system)
		if adapter.name == Gemini {
			systemValue = object(p.Object{"parts": systemValue})
		}
	}
	return array(messages), systemValue, nil
}

func (adapter module) encodeChatMessage(node p.Node, direction p.Direction, options p.EvaluationContext) ([]p.Value, error) {
	fields := p.Object{"role": node.Role}
	if err := adapter.writeMetadata(fields, adapter.contentMetadata([]p.Node{node}), "message"); err != nil {
		return nil, err
	}
	attributes := node.Attributes
	if foreign := foreignWireKeys(attributes, adapter.family); len(foreign) > 0 {
		// 消息级源族扩展（如 Anthropic 的消息级 cache_control）在 Chat 目标
		// 没有等价字段：剥离并显式 warning，不再拒绝整个请求。
		warnDropped(options, p.EncodeRequest, "/content/extensions", "message-level wire extensions dropped: "+strings.Join(foreign, ", ")+" has no Chat equivalent", "Same-family forwarding preserves them through native replay.")
		attributes = sameFamilyExtensions(attributes, adapter.family)
	}
	if err := adapter.preserveExtensions(fields, attributes); err != nil {
		return nil, err
	}
	var content, calls, results []p.Value
	for _, child := range node.Children {
		if err := checkResourceProtocol(child, options); err != nil {
			return nil, err
		}
		switch child.Kind {
		case p.ToolResultNode:
			result, err := adapter.encodeChatToolResult(child, options)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		case p.ToolCallNode:
			if child.Input.Kind != p.JSONInput {
				return nil, unsupported("/content/input", "Chat requires JSON function arguments")
			}
			if hasItemMetadata(child) {
				warnDropped(options, p.EncodeRequest, "/content/call", "function call item metadata, cache markers or resources dropped for the Chat target", "Same-family forwarding preserves them through native replay.")
			}
			call := p.Object{"id": child.CallID, "type": p.StringValue("function"), "function": object(p.Object{"name": child.Name, "arguments": encodeJSONArguments(child.Input.Value)})}
			if err := adapter.preserveExtensions(call, child.Attributes); err != nil {
				return nil, err
			}
			calls = append(calls, object(call))
		case p.ReasoningNode:
			// Chat 只有单一 reasoning_content 字符串：多块拼接；签名/缓存等
			// 跨族不可表达的元数据丢弃并给显式 warning（原为硬拒）。
			if len(child.Children) > 0 {
				return nil, unsupported("/reasoning", "Chat cannot express nested reasoning blocks")
			}
			if len(child.Resources) > 0 || len(child.Attributes) > 0 || len(child.Cache) > 0 || !child.ID.IsZero() || !child.Status.IsZero() {
				warnDropped(options, p.EncodeRequest, "/reasoning", "reasoning signatures or metadata dropped: Chat has no signed reasoning channel", "Same-family forwarding preserves signed reasoning through native replay.")
			}
			if !fields["reasoning_content"].IsZero() {
				previous, err := stringValue(fields["reasoning_content"])
				if err != nil {
					return nil, err
				}
				next, err := stringValue(child.Payload)
				if err != nil {
					return nil, err
				}
				fields["reasoning_content"] = p.StringValue(previous + next)
			} else {
				fields["reasoning_content"] = child.Payload
			}
		case p.RefusalNode:
			if !fields["refusal"].IsZero() || len(child.Attributes) > 0 || len(child.Cache) > 0 {
				return nil, unsupported("/refusal", "Chat refusal scalar cannot carry multiple blocks or block metadata")
			}
			fields["refusal"] = child.Payload
		default:
			block, err := adapter.encodeBlock(child, direction, options)
			if err != nil {
				return nil, err
			}
			content = append(content, block)
		}
	}
	if len(results) > 0 {
		return results, nil
	}
	if len(content) == 1 && direction == p.EncodeResponse {
		block, _ := content[0].ReadObject()
		if block["type"] == p.StringValue("text") {
			fields["content"] = block["text"]
		} else {
			fields["content"] = array(content)
		}
	} else if len(content) == 1 && len(node.Children) == 1 && node.Children[0].Kind == p.TextNode && len(node.Children[0].Cache) == 0 && len(node.Children[0].Attributes) == 0 {
		fields["content"] = node.Children[0].Payload
	} else if content != nil {
		fields["content"] = array(content)
	}
	if calls != nil {
		fields["tool_calls"] = array(calls)
	}
	return []p.Value{object(fields)}, nil
}

// isErrorResult 读取 tool_result 的 Status.isError 标记（Anthropic is_error
// 的语义落点）。仅布尔真值视为错误结果。
func isErrorResult(status p.Value) bool {
	if status.IsZero() {
		return false
	}
	object, err := status.ReadObject()
	if err != nil {
		return false
	}
	flag, exists := object["isError"]
	if !exists || flag.IsZero() {
		return false
	}
	var value bool
	return flag.Decode(&value) == nil && value
}

// foreignWireKeys 列出与目标族不匹配的 wire:* 扩展键。
func foreignWireKeys(attributes p.Object, family string) []string {
	var foreign []string
	for key := range attributes {
		if prefix, ok := strings.CutPrefix(key, wireExtensionPrefix); ok && prefix != family {
			foreign = append(foreign, key)
		}
	}
	return foreign
}

// sameFamilyExtensions 返回仅含目标族 wire:* 扩展的浅拷贝。
func sameFamilyExtensions(attributes p.Object, family string) p.Object {
	remaining := p.Object{}
	for key, value := range attributes {
		if prefix, ok := strings.CutPrefix(key, wireExtensionPrefix); ok && prefix != family {
			continue
		}
		remaining[key] = value
	}
	return remaining
}

// warnDropped 记录一次「跨族不可表达 → 显式剥离/丢弃」的 warning 诊断：
// 本轮点对点保证中所有降级适配共用这一构造。
func warnDropped(options p.EvaluationContext, direction p.Direction, path, reason, suggestion string) {
	options.Diagnostics.Add(p.ConversionIssue{
		Code: p.UnsupportedCapability, Severity: p.SeverityWarning, Protocol: options.Identity(),
		Direction: direction, Stage: "wire", Path: path,
		Reason: reason, Suggestion: suggestion,
	})
}

// hasItemMetadata 判定节点是否携带项目级元数据（id/status/cache/resources）。
func hasItemMetadata(node p.Node) bool {
	return !node.ID.IsZero() || !node.Status.IsZero() || len(node.Cache) > 0 || len(node.Resources) > 0
}

// encodeChatToolResult 把一个工具结果节点转为一条 Chat tool 角色消息：
// 对象载荷 JSON 序列化、结构化块拼接为文本、is_error 加文本标记，节点级
// 元数据剥离并给 warning。
func (adapter module) encodeChatToolResult(child p.Node, options p.EvaluationContext) (p.Value, error) {
	payload := child.Payload
	if payload.IsObject() {
		return p.Value{}, unsupported("/content/result", "object tool result requires tool_result_text conversion")
	}
	if len(child.Children) > 0 {
		var text []byte
		for _, block := range child.Children {
			if block.Kind != p.TextNode {
				return p.Value{}, unsupported("/content/result", "Chat tool result blocks must be text")
			}
			value, err := stringValue(block.Payload)
			if err != nil {
				return p.Value{}, err
			}
			text = append(text, value...)
		}
		payload = p.StringValue(string(text))
	}
	if hasItemMetadata(child) {
		warnDropped(options, p.EncodeRequest, "/content/result",
			"tool result item metadata, cache markers or resources dropped for the Chat target",
			"Same-family forwarding preserves them through native replay.")
	}
	if isErrorResult(child.Status) {
		if text, err := stringValue(payload); err == nil {
			payload = p.StringValue("[Tool error] " + text)
		}
	}
	result := p.Object{"role": p.StringValue("tool"), "tool_call_id": child.CallID, "name": child.Name, "content": payload}
	if err := adapter.preserveExtensions(result, child.Attributes); err != nil {
		return p.Value{}, err
	}
	return object(result), nil
}
