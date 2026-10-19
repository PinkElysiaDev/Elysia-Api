package builtin

import (
	"fmt"
	"strconv"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) decodeMessages(value p.Value, path string, direction p.Direction, options p.EvaluationContext, history *historyState) ([]p.Node, error) {
	items, err := readContextArray(value, options)
	if err != nil {
		return nil, err
	}
	nodes := make([]p.Node, 0, len(items))
	for index, item := range items {
		location := fmt.Sprintf("%s/%d", path, index)
		fields, err := options.ReadObject(item)
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
		known := []string{"type", "role", "content"}
		if adapter.name == Gemini {
			content = fields["parts"]
			known = []string{"role", "parts"}
			if role == "model" {
				role = "assistant"
			}
		}
		node := p.Node{Kind: p.MessageNode, Role: p.StringValue(role), Native: adapter.native(item, location, direction, options)}
		if adapter.name == Responses {
			node.ID, node.Status = fields["id"], fields["status"]
			known = append(known, "id", "status")
		}
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
			// In a tool-only assistant message, a scalar null/empty content is
			// Chat's no-text envelope, not a separate part before the tool call.
			// Preserve the native wire value, including null versus empty string.
			if role == "assistant" && len(calls) > 0 && (content.IsNull() || content.IsString("")) {
				node.Children = nil
			}
			for callIndex, callValue := range calls {
				call, err := options.ReadObject(callValue)
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
				// Some Chat providers repeat the array position in complete JSON
				// tool calls. Only an exact positional index is redundant; do not
				// discard an alternative association or malformed vendor value.
				if index := call["index"]; !index.IsZero() {
					var observed int
					if index.IsNull() || index.Decode(&observed) != nil || observed != callIndex {
						return nil, p.IssuesError([]p.ConversionIssue{{Code: p.InvalidAssociation, Severity: p.SeverityError, Path: fmt.Sprintf("%s/tool_calls/%d/index", location, callIndex), Reason: "complete tool call index must match its array position"}})
					}
				}
				function, err := options.ReadObject(call["function"])
				if err != nil {
					return nil, err
				}
				arguments, err := readJSONArguments(function["arguments"])
				if err != nil {
					return nil, err
				}
				attributes, err := adapter.nestedExtensions(call, []string{"type", "id", "function", "index"}, map[string][]string{"function": {"name", "arguments"}}, options)
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, p.Node{Kind: p.ToolCallNode, Name: function["name"], CallID: call["id"], Input: &p.ToolInput{Kind: p.JSONInput, Value: arguments}, Native: adapter.native(callValue, fmt.Sprintf("%s/tool_calls/%d", location, callIndex), direction, options), Attributes: attributes})
			}
			known = append(known, "tool_calls", "reasoning_content", "refusal")
			if reasoning := fields["reasoning_content"]; !reasoning.IsZero() && !reasoning.IsNull() && !reasoning.IsString("") {
				node.Children = append([]p.Node{{Kind: p.ReasoningNode, Payload: reasoning}}, node.Children...)
			}
			// Chat SDK history includes refusal:null for ordinary assistant
			// messages. Null denotes no refusal; native snapshots keep presence.
			if refusal := fields["refusal"]; !refusal.IsZero() && !refusal.IsNull() {
				if _, err := stringValue(refusal); err != nil {
					return nil, p.IssuesError([]p.ConversionIssue{{Code: p.InvalidInput, Severity: p.SeverityError, Path: location + "/refusal", Reason: "Chat refusal requires a string or null"}})
				}
				node.Children = append(node.Children, p.Node{Kind: p.RefusalNode, Payload: refusal})
			}
		}
		node.Cache, err = decodeCache(fields, "message")
		if err != nil {
			return nil, err
		}
		known = append(known, "cache_control")
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
	fields, err := options.ReadObject(value)
	if err != nil {
		return p.Node{}, err
	}
	if kind, err := optionalString(fields["type"]); err != nil {
		return p.Node{}, err
	} else if kind == "reasoning" {
		node, err := adapter.decodeReasoningItem(value, path, direction, options)
		return p.CanonicalReasoning(node), err
	}
	return adapter.decodeBlock(value, path, direction, options, history)
}

func (adapter module) encodeMessages(nodes []p.Node, direction p.Direction, options p.EvaluationContext) (p.Value, p.Value, error) {
	if direction == p.EncodeRequest && (adapter.name == Anthropic || adapter.name == Gemini) {
		count, bare := 0, false
		for i, n := range nodes {
			if n.Kind == p.ReasoningNode || n.Kind == p.ToolCallNode || (n.Kind == p.MessageNode && n.Role.IsString("assistant")) {
				count++
				bare = bare || n.Kind != p.MessageNode
				if count > 1 && bare {
					return p.Value{}, p.Value{}, unsupported(fmt.Sprintf("/content/%d", i), "independent assistant history items require assistant_history projection before block encoding")
				}
			} else {
				count, bare = 0, false
			}
		}
	}
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
		isNativeSystemMessage := adapter.name == Anthropic && direction == p.EncodeRequest && role == "system" && hasNativeSystemMessage(node, options)
		if !isNativeSystemMessage && (role == "system" || role == "developer") && (adapter.name == Anthropic || adapter.name == Gemini) {
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
			return p.Value{}, p.Value{}, unsupported(fmt.Sprintf("/content/%d", index), "message item identity/status requires explicit history projection")
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
		if len(node.Resources) > 0 {
			return p.Value{}, p.Value{}, unsupported("/content/resources", "message resources require explicit mapping")
		}
		if err := adapter.encodeCache(fields, node.Cache); err != nil {
			return p.Value{}, p.Value{}, err
		}
		if err := adapter.writeMetadata(fields, adapter.contentMetadata([]p.Node{node}), "message"); err != nil {
			return p.Value{}, p.Value{}, err
		}
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
			blockDirection := direction
			if adapter.name == Responses && role == "assistant" && child.Kind == p.TextNode {
				// Assistant history is model output even inside a new request.
				blockDirection = p.EncodeResponse
			}
			block, err := adapter.encodeBlock(child, blockDirection, options)
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
			if len(node.Children) == 0 {
				fields["type"], fields["content"] = p.StringValue("message"), array(nil)
				messages = append(messages, object(fields))
			}
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

// A system message and a top-level system envelope have different scope. Only
// an original message from the matching wire can retain its inline placement;
// foreign or newly authored instructions still need the semantic hoist rule.
func hasNativeSystemMessage(node p.Node, options p.EvaluationContext) bool {
	if node.Native == nil || !p.CanPreserveNative(node.Native.Source, p.Target{Protocol: options.Identity(), Direction: p.EncodeRequest}) {
		return false
	}
	index, hasMessagePath := strings.CutPrefix(node.Native.Source.Path, "/messages/")
	if !hasMessagePath {
		return false
	}
	position, err := strconv.Atoi(index)
	if err != nil || position < 0 {
		return false
	}
	fields, err := node.Native.Value.ReadObject()
	return err == nil && fields["role"].IsString("system")
}

func (adapter module) encodeChatMessage(node p.Node, direction p.Direction, options p.EvaluationContext) ([]p.Value, error) {
	if direction == p.EncodeRequest && node.Role.IsString("assistant") {
		previous, thoughts := -1, 0
		for _, child := range node.Children {
			rank := 1
			switch child.Kind {
			case p.ReasoningNode:
				rank, thoughts = 0, thoughts+1
			case p.ToolCallNode:
				rank = 2
			case p.RefusalNode:
				rank = 3
			}
			if rank < previous || thoughts > 1 {
				return nil, unsupported("/content", "Chat history requires an explicit chat_history projection for interleaved content or multiple thinking blocks")
			}
			previous = rank
		}
	}
	fields := p.Object{"role": node.Role}
	if err := adapter.writeMetadata(fields, adapter.contentMetadata([]p.Node{node}), "message"); err != nil {
		return nil, err
	}
	if len(node.Resources) > 0 {
		return nil, unsupported("/content/resources", "message resources require explicit mapping")
	}
	if err := adapter.encodeCache(fields, node.Cache); err != nil {
		return nil, err
	}
	if err := adapter.preserveExtensions(fields, node.Attributes); err != nil {
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
				return nil, unsupported("/content/call", "function call metadata or resources require explicit projection")
			}
			call := p.Object{"id": child.CallID, "type": p.StringValue("function"), "function": object(p.Object{"name": child.Name, "arguments": encodeJSONArguments(child.Input.Value)})}
			if err := adapter.preserveExtensions(call, child.Attributes); err != nil {
				return nil, err
			}
			calls = append(calls, object(call))
		case p.ReasoningNode:
			if child.ReasoningForm != "" || child.ReasoningContent != nil {
				return nil, unsupported("/reasoningForm", "structured reasoning requires an explicit projection to Chat visible thinking")
			}
			if len(child.Children) > 0 {
				return nil, unsupported("/reasoning", "Chat cannot express nested reasoning blocks")
			}
			if len(child.Resources) > 0 || len(child.Attributes) > 0 || len(child.Cache) > 0 || !child.ID.IsZero() || !child.Status.IsZero() {
				return nil, unsupported("/reasoning", "reasoning metadata or resources require explicit projection")
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
		if len(results) != len(node.Children) {
			return nil, unsupported("/content", "mixed tool results require explicit ordered history projection")
		}
		return results, nil
	}
	if len(content) == 1 && direction == p.EncodeResponse {
		block, _ := content[0].ReadObject()
		if block["type"].IsString("text") {
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

func hasItemMetadata(node p.Node) bool {
	return !node.ID.IsZero() || !node.Status.IsZero() || len(node.Cache) > 0 || len(node.Resources) > 0
}

func (adapter module) encodeChatToolResult(child p.Node, options p.EvaluationContext) (p.Value, error) {
	payload := child.Payload
	if payload.IsObject() {
		return p.Value{}, unsupported("/content/result", "object tool result requires tool_result_text conversion")
	}
	if len(child.Children) > 0 {
		var text []byte
		for _, block := range child.Children {
			if block.Kind != p.TextNode || hasItemMetadata(block) || len(block.Attributes) > 0 || len(block.Metadata) > 0 {
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
	if !child.ID.IsZero() || len(child.Cache) > 0 || len(child.Resources) > 0 || !child.Status.IsZero() {
		return p.Value{}, unsupported("/content/result", "tool result metadata or resources require explicit projection")
	}
	result := p.Object{"role": p.StringValue("tool"), "tool_call_id": child.CallID, "name": child.Name, "content": payload}
	if err := adapter.preserveExtensions(result, child.Attributes); err != nil {
		return p.Value{}, err
	}
	return object(result), nil
}
