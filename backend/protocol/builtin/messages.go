package builtin

import (
	"fmt"

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
		role, err := stringValue(fields["role"])
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
			calls, err := readArray(fields["tool_calls"])
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
		node.Attributes = adapter.extensions(fields, known)
		nodes = append(nodes, node)
	}
	return nodes, nil
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
	for _, node := range nodes {
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
		role, err := stringValue(node.Role)
		if err != nil {
			return p.Value{}, p.Value{}, err
		}
		if (role == "system" || role == "developer") && (adapter.name == Anthropic || adapter.name == Gemini) {
			if !node.ID.IsZero() || !node.Status.IsZero() || len(node.Attributes) > 0 || len(node.Cache) > 0 || len(node.Resources) > 0 {
				return p.Value{}, p.Value{}, unsupported("/content/system", "target system container cannot carry message metadata; use explicit block mappings")
			}
			if hasConversation {
				return p.Value{}, p.Value{}, unsupported("/content", "target cannot represent a system message after conversation content")
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
			return p.Value{}, p.Value{}, unsupported("/content/message", "target messages have no equivalent item identity or status")
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
	if err := adapter.preserveExtensions(fields, node.Attributes); err != nil {
		return nil, err
	}
	var content, calls, results []p.Value
	hasCall := false
	for _, child := range node.Children {
		if err := checkResourceProtocol(child, options); err != nil {
			return nil, err
		}
		switch child.Kind {
		case p.ToolResultNode:
			if len(node.Children) != 1 {
				return nil, unsupported("/content", "Chat tool results require their own message")
			}
			payload := child.Payload
			if payload.IsObject() {
				return nil, unsupported("/content", "Chat tool result cannot represent a JSON object without an explicit serialization mapping")
			}
			if len(child.Children) > 0 {
				return nil, unsupported("/content", "Chat tool result cannot express structured content blocks")
			}
			if !child.ID.IsZero() || !child.Status.IsZero() || len(child.Cache) > 0 || len(child.Resources) > 0 {
				return nil, unsupported("/content/result", "Chat tool results cannot carry item metadata or cache boundaries")
			}
			result := p.Object{"role": p.StringValue("tool"), "tool_call_id": child.CallID, "name": child.Name, "content": payload}
			if err := adapter.preserveExtensions(result, child.Attributes); err != nil {
				return nil, err
			}
			results = append(results, object(result))
		case p.ToolCallNode:
			if child.Input.Kind != p.JSONInput {
				return nil, unsupported("/content/input", "Chat requires JSON function arguments")
			}
			if !child.ID.IsZero() || !child.Status.IsZero() || len(child.Cache) > 0 || len(child.Resources) > 0 {
				return nil, unsupported("/content/call", "Chat function calls cannot carry a separate item identity, status or cache boundary")
			}
			call := p.Object{"id": child.CallID, "type": p.StringValue("function"), "function": object(p.Object{"name": child.Name, "arguments": p.StringValue(string(child.Input.Value.Bytes()))})}
			if err := adapter.preserveExtensions(call, child.Attributes); err != nil {
				return nil, err
			}
			calls = append(calls, object(call))
			hasCall = true
		case p.ReasoningNode:
			if !fields["reasoning_content"].IsZero() || len(child.Children) > 0 || len(child.Resources) > 0 || len(child.Attributes) > 0 || len(child.Cache) > 0 || !child.ID.IsZero() || !child.Status.IsZero() {
				return nil, unsupported("/reasoning", "Chat cannot collapse multiple reasoning blocks, summaries or signed payloads")
			}
			fields["reasoning_content"] = child.Payload
		case p.RefusalNode:
			if !fields["refusal"].IsZero() || len(child.Attributes) > 0 || len(child.Cache) > 0 {
				return nil, unsupported("/refusal", "Chat refusal scalar cannot carry multiple blocks or block metadata")
			}
			fields["refusal"] = child.Payload
		default:
			if hasCall {
				return nil, unsupported("/content", "Chat cannot preserve content after an interleaved tool call")
			}
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
	if len(content) == 1 && len(node.Children) == 1 && node.Children[0].Kind == p.TextNode && len(node.Children[0].Cache) == 0 && len(node.Children[0].Attributes) == 0 {
		fields["content"] = node.Children[0].Payload
	} else if content != nil {
		fields["content"] = array(content)
	}
	if calls != nil {
		fields["tool_calls"] = array(calls)
	}
	return []p.Value{object(fields)}, nil
}
