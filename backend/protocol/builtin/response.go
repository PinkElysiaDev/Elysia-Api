package builtin

import (
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) decodeResponse(input p.Value, options p.EvaluationContext) (*p.Response, error) {
	fields, err := input.ReadObject()
	if err != nil {
		return nil, err
	}
	if hasErrorEnvelope(fields) {
		return adapter.decodeErrorEnvelope(fields, options)
	}
	response := &p.Response{SchemaVersion: p.SemanticSchemaVersion, Source: options.Identity(), ID: fields["id"], Model: fields["model"], Status: fields["status"], Error: fields["error"], Content: []p.Node{}, Attributes: p.Object{}}
	if !response.Error.IsZero() && !response.Error.IsNull() {
		response.Error, err = adapter.decodeFailure(response.Error, options)
		if err != nil {
			return nil, err
		}
	}
	history := &historyState{calls: map[string][]p.Value{}}
	known := []string{"id", "model", "status", "error", "usage", "object", "created", "created_at"}
	usage := fields["usage"]
	// 首个非零者生效：Chat 的 created（秒）与 Responses 的 created_at（ISO）同义。
	if created := fields["created_at"]; !created.IsZero() {
		response.Attributes["created_at"] = created
	} else if created := fields["created"]; !created.IsZero() {
		response.Attributes["created_at"] = created
	}
	switch adapter.name {
	case Chat:
		known = append(known, "choices")
		choices, err := readArray(fields["choices"])
		if err != nil {
			return nil, err
		}
		for index, choice := range choices {
			entry, err := choice.ReadObject()
			if err != nil {
				return nil, err
			}
			nodes, err := adapter.decodeMessages(array([]p.Value{entry["message"]}), fmt.Sprintf("/choices/%d/message", index), p.DecodeResponse, options, history)
			if err != nil {
				return nil, err
			}
			if len(choices) > 1 {
				if nodes[0].Attributes == nil {
					nodes[0].Attributes = p.Object{}
				}
				nodes[0].Attributes["choiceIndex"], nodes[0].Attributes["finishReason"] = entry["index"], entry["finish_reason"]
			} else {
				response.Attributes["finishReason"] = entry["finish_reason"]
			}
			if extra := collectUnknown(entry, []string{"index", "message", "finish_reason"}); !extra.IsZero() {
				response.Attributes[wireExtensionPrefix+adapter.family] = object(p.Object{"choiceExtensions": extra})
			}
			response.Content = append(response.Content, nodes...)
		}
		response.Status = p.StringValue("completed")
	case Anthropic:
		known = append(known, "type", "role", "content", "stop_reason", "stop_sequence")
		children, err := adapter.decodeContent(fields["content"], "/content", p.DecodeResponse, options, history)
		if err != nil {
			return nil, err
		}
		response.Content = []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: children}}
		response.Status = p.StringValue("completed")
		if reason := fields["stop_reason"]; !reason.IsZero() {
			response.Attributes["finishReason"], err = decodeFinishReason(adapter.name, reason)
			if err != nil {
				return nil, err
			}
		}
		if stop := fields["stop_sequence"]; !stop.IsZero() {
			response.Attributes["anthropic_stop_sequence"] = stop
		}
	case Responses:
		known = append(known, "output", "incomplete_details")
		response.Content, err = adapter.decodeMessages(fields["output"], "/output", p.DecodeResponse, options, history)
		if err != nil {
			return nil, err
		}
		response.Attributes["finishReason"], err = decodeResponsesFinish(response, fields["incomplete_details"])
		if err != nil {
			return nil, err
		}
	case Gemini:
		known = append(known, "candidates", "responseId", "modelVersion", "usageMetadata")
		response.ID, response.Model, response.Status = fields["responseId"], fields["modelVersion"], p.StringValue("completed")
		usage = fields["usageMetadata"]
		candidates, err := readArray(fields["candidates"])
		if err != nil {
			return nil, err
		}
		for index, candidate := range candidates {
			entry, err := candidate.ReadObject()
			if err != nil {
				return nil, err
			}
			content, err := entry["content"].ReadObject()
			if err != nil {
				return nil, err
			}
			if content["role"].IsZero() {
				content["role"] = p.StringValue("model")
			}
			nodes, err := adapter.decodeMessages(array([]p.Value{object(content)}), fmt.Sprintf("/candidates/%d/content", index), p.DecodeResponse, options, history)
			if err != nil {
				return nil, err
			}
			reason, err := decodeFinishReason(adapter.name, entry["finishReason"])
			if err != nil {
				return nil, err
			}
			if string(reason.Bytes()) == `"stop"` && hasToolCalls(nodes) {
				reason = p.StringValue("tool_calls")
			}
			if len(candidates) > 1 {
				if nodes[0].Attributes == nil {
					nodes[0].Attributes = p.Object{}
				}
				nodes[0].Attributes["choiceIndex"], nodes[0].Attributes["finishReason"] = entry["index"], reason
			} else {
				response.Attributes["finishReason"] = reason
			}
			if extra := collectUnknown(entry, []string{"content", "index", "finishReason"}); !extra.IsZero() {
				nodes[0].Attributes = mergeAttributes(nodes[0].Attributes, adapter.extensions(entry, []string{"content", "index", "finishReason"}))
			}
			response.Content = append(response.Content, nodes...)
		}
	}
	response.Usage, err = adapter.decodeUsage(usage)
	if err != nil {
		return nil, err
	}
	response.Attributes = mergeAttributes(response.Attributes, adapter.extensions(fields, known))
	usageExtra, err := adapter.usageExtensions(usage)
	if err != nil {
		return nil, err
	}
	if !usageExtra.IsZero() {
		key := "wire:" + adapter.family
		extra := p.Object{}
		if value := response.Attributes[key]; !value.IsZero() {
			extra, err = value.ReadObject()
			if err != nil {
				return nil, err
			}
		}
		field := "usage"
		if adapter.name == Gemini {
			field = "usageMetadata"
		}
		extra[field] = usageExtra
		response.Attributes[key] = object(extra)
	}
	for key, value := range response.Attributes {
		if value.IsZero() {
			delete(response.Attributes, key)
		}
	}
	return response, nil
}

func mergeAttributes(left, right p.Object) p.Object {
	if left == nil {
		left = p.Object{}
	}
	for key, value := range right {
		left[key] = value
	}
	return left
}

var finishReasons = map[string]map[string]string{
	Chat:      {"stop": "stop", "length": "length", "tool_calls": "tool_calls", "content_filter": "content_filter"},
	Anthropic: {"end_turn": "stop", "stop_sequence": "stop", "max_tokens": "length", "tool_use": "tool_calls"},
	Gemini:    {"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "content_filter", "RECITATION": "recitation", "OTHER": "other"},
}

// Responses expresses completion through status and incomplete_details. Tool
// handoff is determined from its output, not invented from a missing stop field.
func decodeResponsesFinish(response *p.Response, details p.Value) (p.Value, error) {
	status, err := optionalString(response.Status)
	if err != nil {
		return p.Value{}, err
	}
	switch status {
	case "completed":
		if hasToolCalls(response.Content) {
			return p.StringValue("tool_calls"), nil
		}
		return p.StringValue("stop"), nil
	case "incomplete":
		fields, err := details.ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		reason, err := stringValue(fields["reason"])
		if err != nil {
			return p.Value{}, err
		}
		switch reason {
		case "max_output_tokens":
			return p.StringValue("length"), nil
		case "content_filter":
			return p.StringValue("content_filter"), nil
		default:
			return p.Value{}, unsupported("/incomplete_details/reason", "unmapped incomplete response reason: "+reason)
		}
	case "", "queued", "in_progress", "failed", "cancelled":
		return p.Value{}, nil
	default:
		return p.Value{}, unsupported("/status", "unmapped response status: "+status)
	}
}

func hasToolCalls(nodes []p.Node) bool {
	for _, node := range nodes {
		if node.Kind == p.ToolCallNode || hasToolCalls(node.Children) {
			return true
		}
	}
	return false
}

func encodeResponsesFinish(response *p.Response) (p.Value, p.Value, error) {
	reason, err := optionalString(response.Attributes["finishReason"])
	if err != nil {
		return p.Value{}, p.Value{}, err
	}
	switch reason {
	case "":
		return response.Status, p.Value{}, nil
	case "stop", "tool_calls":
		return p.StringValue("completed"), p.Value{}, nil
	case "length":
		return p.StringValue("incomplete"), object(p.Object{"reason": p.StringValue("max_output_tokens")}), nil
	case "content_filter":
		return p.StringValue("incomplete"), object(p.Object{"reason": p.StringValue("content_filter")}), nil
	default:
		return p.Value{}, p.Value{}, unsupported("/finishReason", "Responses cannot express this terminal reason")
	}
}

func decodeFinishReason(name string, value p.Value) (p.Value, error) {
	if value.IsZero() || value.IsNull() {
		return value, nil
	}
	text, err := stringValue(value)
	if err != nil {
		return p.Value{}, err
	}
	if reason, exists := finishReasons[name][text]; exists {
		return p.StringValue(reason), nil
	}
	return p.StringValue(name + ":" + text), nil
}

func encodeFinishReason(name string, value p.Value) (p.Value, error) {
	if value.IsZero() || value.IsNull() {
		return value, nil
	}
	text, err := stringValue(value)
	if err != nil {
		return p.Value{}, err
	}
	// Canonical values with multiple equivalent wire values use one stable spelling.
	if name == Anthropic && text == "stop" {
		return p.StringValue("end_turn"), nil
	}
	if name == Gemini && text == "tool_calls" {
		return p.StringValue("STOP"), nil
	}
	for wire, semantic := range finishReasons[name] {
		if semantic == text {
			return p.StringValue(wire), nil
		}
	}
	// Gemini 独有的 RECITATION/OTHER：映射到各目标的最近语义而非拒绝，
	// 行为由成对矩阵测试与保证文档钉死。
	if fallback, exists := finishReasonFallbacks[text][name]; exists {
		return p.StringValue(fallback), nil
	}
	prefix := name + ":"
	if len(text) > len(prefix) && text[:len(prefix)] == prefix {
		return p.StringValue(text[len(prefix):]), nil
	}
	return p.Value{}, unsupported("/finishReason", "target cannot express this terminal reason")
}

var finishReasonFallbacks = map[string]map[string]string{
	"recitation": {Chat: "content_filter", Anthropic: "refusal", Responses: "content_filter", Gemini: "RECITATION"},
	"other":      {Chat: "stop", Anthropic: "end_turn", Responses: "stop", Gemini: "OTHER"},
}

func finishReasonOf(response *p.Response) (p.Value, error) {
	if reason := response.Attributes["finishReason"]; !reason.IsZero() {
		return reason, nil
	}
	return decodeResponsesFinish(response, p.Value{})
}

func (adapter module) encodeResponse(response *p.Response, options p.EvaluationContext) (p.Value, error) {
	fields := p.Object{"id": response.ID, "model": response.Model, "error": response.Error}
	if !response.Error.IsZero() && !response.Error.IsNull() {
		failure, err := adapter.encodeFailure(response.Error, options)
		if err != nil {
			return p.Value{}, err
		}
		fields["error"] = failure
	}
	if err := adapter.preserveExtensions(fields, response.Attributes); err != nil {
		return p.Value{}, err
	}
	usage, err := adapter.encodeResponseUsage(response, options)
	if err != nil {
		return p.Value{}, err
	}
	usageField := "usage"
	if adapter.name == Gemini {
		usageField = "usageMetadata"
	}
	usage, err = mergeUsageExtensions(usage, fields[usageField])
	if err != nil {
		return p.Value{}, err
	}
	fields["usage"] = usage
	if !response.Error.IsZero() && !response.Error.IsNull() && len(response.Content) == 0 {
		fields["status"] = response.Status
		if adapter.name == Anthropic {
			fields["type"] = p.StringValue("error")
		}
		if adapter.name == Gemini {
			delete(fields, "usage")
			fields["usageMetadata"] = usage
		}
		return object(fields), nil
	}
	reason, err := finishReasonOf(response)
	if err != nil {
		return p.Value{}, err
	}
	finish, err := encodeFinishReason(adapter.name, reason)
	if err != nil && adapter.name != Responses {
		return p.Value{}, err
	}
	nodes := response.Content
	if adapter.name != Responses {
		nodes, err = groupResponseOutput(nodes)
		if err != nil {
			return p.Value{}, err
		}
	} else {
		nodes = groupResponsesContent(nodes)
	}
	content, _, err := adapter.encodeMessages(nodes, p.EncodeResponse, options)
	if err != nil {
		return p.Value{}, err
	}
	switch adapter.name {
	case Responses:
		fields["object"], fields["status"], fields["output"], fields["created_at"] = p.StringValue("response"), response.Status, content, response.Attributes["created_at"]
		fields["status"], fields["incomplete_details"], err = encodeResponsesFinish(response)
		if err != nil {
			return p.Value{}, err
		}
	case Chat, Gemini:
		messages, err := readArray(content)
		if err != nil {
			return p.Value{}, err
		}
		var choices []p.Value
		for index, message := range messages {
			position, _ := p.EncodeValue(index)
			entry := p.Object{"index": position}
			if adapter.name == Gemini && len(messages) == 1 {
				delete(entry, "index")
			}
			if adapter.name == Chat {
				entry["message"], entry["finish_reason"] = message, finish
			} else {
				entry["content"], entry["finishReason"] = message, finish
			}
			choices = append(choices, object(entry))
		}
		if adapter.name == Chat {
			fields["object"], fields["created"], fields["choices"] = p.StringValue("chat.completion"), response.Attributes["created_at"], array(choices)
		} else {
			delete(fields, "id")
			delete(fields, "model")
			delete(fields, "usage")
			fields["responseId"], fields["modelVersion"], fields["usageMetadata"], fields["candidates"] = response.ID, response.Model, usage, array(choices)
		}
	case Anthropic:
		messages, err := readArray(content)
		if err != nil {
			return p.Value{}, err
		}
		if len(messages) != 1 {
			return p.Value{}, unsupported("/content", "Anthropic response requires one assistant content group")
		}
		message, err := messages[0].ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		fields["type"], fields["role"], fields["content"], fields["stop_reason"], fields["stop_sequence"] = p.StringValue("message"), p.StringValue("assistant"), message["content"], finish, response.Attributes["anthropic_stop_sequence"]
	}
	return object(fields), nil
}

func hasErrorEnvelope(fields p.Object) bool {
	if fields["error"].IsZero() || fields["error"].IsNull() {
		return false
	}
	for _, key := range []string{"choices", "output", "content", "candidates"} {
		if !fields[key].IsZero() {
			return false
		}
	}
	return true
}

func (adapter module) decodeErrorEnvelope(fields p.Object, options p.EvaluationContext) (*p.Response, error) {
	usageField := "usage"
	if adapter.name == Gemini {
		usageField = "usageMetadata"
	}
	usage, err := adapter.decodeUsage(fields[usageField])
	if err != nil {
		return nil, err
	}
	failure, err := adapter.decodeFailure(fields["error"], options)
	if err != nil {
		return nil, err
	}
	return &p.Response{SchemaVersion: p.SemanticSchemaVersion, Source: options.Identity(), ID: fields["id"], Model: fields["model"], Status: fields["status"], Error: failure, Content: []p.Node{}, Usage: usage,
		Attributes: adapter.extensions(fields, []string{"id", "model", "status", "error", "type", usageField})}, nil
}

// A generated response is one assistant turn. Separate Responses output items
// remain ordered within that turn; they are not additional Chat choices.
func groupResponseOutput(nodes []p.Node) ([]p.Node, error) {
	if len(nodes) == 1 && nodes[0].Kind == p.MessageNode {
		return nodes, nil
	}
	message := p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant")}
	for _, node := range nodes {
		if node.Kind != p.MessageNode {
			message.Children = append(message.Children, node)
			continue
		}
		role, err := stringValue(node.Role)
		if err != nil {
			return nil, err
		}
		if role != "assistant" || !node.ID.IsZero() || !node.Status.IsZero() || len(node.Attributes) > 0 || len(node.Cache) > 0 || len(node.Resources) > 0 {
			return nil, unsupported("/content", "target cannot combine independently identified or annotated output messages")
		}
		message.Children = append(message.Children, node.Children...)
	}
	return []p.Node{message}, nil
}

func groupResponsesContent(nodes []p.Node) []p.Node {
	var output, content []p.Node
	flush := func() {
		if len(content) > 0 {
			output = append(output, p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: content})
			content = nil
		}
	}
	for _, node := range nodes {
		switch node.Kind {
		case p.MessageNode, p.ToolCallNode, p.ReasoningNode, p.OpaqueNode:
			flush()
			output = append(output, node)
		default:
			content = append(content, node)
		}
	}
	flush()
	return output
}
