package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

var audioFormats = map[string]string{"wav": "audio/wav", "mp3": "audio/mpeg"}

func (adapter module) encodeBlock(node p.Node, direction p.Direction, options p.EvaluationContext) (p.Value, error) {
	if err := checkResourceProtocol(node, options); err != nil {
		return p.Value{}, err
	}
	if node.ReasoningForm == "summary" && adapter.name != Responses {
		return p.Value{}, unsupported("/reasoningForm", "reasoning summaries cannot be substituted for visible thinking")
	}
	if node.Kind == p.OpaqueNode {
		return adapter.replay(node.Native, direction, options)
	}
	fields := p.Object{}
	if adapter.name == Gemini {
		return adapter.encodeGeminiPart(node, fields)
	}
	switch node.Kind {
	case p.TextNode:
		kind := "text"
		if adapter.name == Responses {
			kind = "input_text"
			if direction == p.EncodeResponse {
				kind = "output_text"
			}
		}
		fields["type"], fields["text"] = p.StringValue(kind), node.Payload
	case p.RefusalNode:
		if adapter.name == Anthropic {
			return p.Value{}, unsupported("/content/refusal", "Anthropic has no equivalent refusal content block")
		}
		fields["type"], fields["refusal"] = p.StringValue("refusal"), node.Payload
	case p.ToolCallNode:
		if adapter.name == Chat {
			return p.Value{}, unsupported("/content", "Chat calls belong to message.tool_calls")
		}
		fields["name"] = node.Name
		if adapter.name == Anthropic {
			if node.Input.Kind != p.JSONInput {
				return p.Value{}, unsupported("/input", "Anthropic requires JSON tool input")
			}
			fields["type"], fields["id"], fields["input"] = p.StringValue("tool_use"), node.CallID, node.Input.Value
		} else {
			fields["call_id"], fields["id"], fields["status"] = node.CallID, node.ID, node.Status
			if node.Input.Kind == p.TextInput {
				fields["type"], fields["input"] = p.StringValue("custom_tool_call"), node.Input.Value
			} else {
				fields["type"], fields["arguments"] = p.StringValue("function_call"), encodeJSONArguments(node.Input.Value)
			}
		}
	case p.ToolResultNode:
		fields["id"] = node.ID
		payload := node.Payload
		if payload.IsObject() {
			// Gemini 标准 functionResponse.response 是对象；无对象载荷字段的
			// 目标（Responses 输出串、Anthropic 内容串/块）统一 JSON 字符串
			// 序列化保持可逆，Chat 路径在 encodeChatMessage 内同构处理。
			payload = encodeJSONArguments(payload)
		}
		if node.Children != nil {
			var blocks []p.Value
			for _, child := range node.Children {
				block, err := adapter.encodeBlock(child, direction, options)
				if err != nil {
					return p.Value{}, err
				}
				blocks = append(blocks, block)
			}
			payload = array(blocks)
		}
		if adapter.name == Anthropic {
			fields["type"], fields["tool_use_id"], fields["content"] = p.StringValue("tool_result"), node.CallID, payload
			if !node.Status.IsZero() {
				status, err := node.Status.ReadObject()
				if err != nil {
					return p.Value{}, err
				}
				fields["is_error"] = status["isError"]
			}
		} else if adapter.name == Responses {
			if !node.Status.IsZero() {
				return p.Value{}, unsupported("/status", "Responses tool output has no separate error flag")
			}
			fields["type"], fields["call_id"], fields["output"] = p.StringValue("function_call_output"), node.CallID, payload
			if node.Attributes["inputKind"] == p.StringValue(string(p.TextInput)) {
				fields["type"] = p.StringValue("custom_tool_call_output")
			}
		} else {
			return p.Value{}, unsupported("/content", "Chat results belong to separate tool messages")
		}
	case p.ReasoningNode:
		if adapter.name == Chat {
			return p.Value{}, unsupported("/content", "Chat reasoning belongs to message.reasoning_content")
		}
		if adapter.name == Responses && isNativeBlock(node, "reasoning_text", direction, options) {
			fields["type"], fields["text"] = p.StringValue("reasoning_text"), node.Payload
			break
		}
		if adapter.name == Anthropic {
			fields["type"], fields["thinking"] = p.StringValue("thinking"), node.Payload
			if len(node.Children) > 0 {
				return p.Value{}, unsupported("/reasoning", "summary blocks cannot be substituted for visible thinking")
			}
		} else {
			if !node.Payload.IsZero() {
				return p.Value{}, unsupported("/reasoning", "visible thinking cannot be substituted for a Responses reasoning summary")
			}
			fields["type"], fields["id"], fields["status"] = p.StringValue("reasoning"), node.ID, node.Status
			var summary []p.Value
			for _, child := range node.Children {
				if child.Kind != p.TextNode {
					return p.Value{}, unsupported("/reasoning", "Responses reasoning summaries require text")
				}
				part := p.Object{"type": p.StringValue("summary_text"), "text": child.Payload}
				if err := adapter.preserveExtensions(part, child.Attributes); err != nil {
					return p.Value{}, err
				}
				summary = append(summary, object(part))
			}
			fields["summary"] = array(summary)
		}
	case p.ImageNode, p.AudioNode, p.DocumentNode, p.VideoNode:
		if err := adapter.encodeMedia(node, fields); err != nil {
			return p.Value{}, err
		}
	default:
		return p.Value{}, unsupported("/content/kind", "unsupported content kind "+string(node.Kind))
	}
	for _, resource := range node.Resources {
		switch resource.Kind {
		case "signature":
			if adapter.name != Anthropic || node.Kind != p.ReasoningNode {
				return p.Value{}, unsupported("/resources", "signature is specific to its source protocol")
			}
			fields["signature"] = resource.ID
		case "encrypted_content":
			if node.Kind != p.ReasoningNode {
				return p.Value{}, unsupported("/resources", "encrypted reasoning requires a reasoning node")
			}
			if adapter.name == Responses {
				fields["encrypted_content"] = resource.ID
			} else {
				fields["type"], fields["data"] = p.StringValue("redacted_thinking"), resource.ID
				delete(fields, "thinking")
			}
		case "file", "file_id":
			if adapter.name == Chat && node.Kind == p.DocumentNode {
				file, err := fields["file"].ReadObject()
				if err != nil {
					return p.Value{}, err
				}
				file["file_id"] = resource.ID
				fields["file"] = object(file)
				continue
			}
			if adapter.name != Responses {
				return p.Value{}, unsupported("/resources", "target has no equivalent file reference")
			}
			fields["file_id"] = resource.ID
		default:
			return p.Value{}, unsupported("/resources", "unsupported resource reference")
		}
	}
	if err := adapter.encodeCache(fields, node.Cache); err != nil {
		return p.Value{}, err
	}
	if err := adapter.preserveExtensions(fields, node.Attributes); err != nil {
		return p.Value{}, err
	}
	return object(fields), nil
}

func isNativeBlock(node p.Node, kind string, direction p.Direction, options p.EvaluationContext) bool {
	if node.Native == nil || !p.CanPreserveNative(node.Native.Source, p.Target{Protocol: options.Identity(), Direction: direction}) {
		return false
	}
	fields, err := node.Native.Value.ReadObject()
	return err == nil && fields["type"] == p.StringValue(kind)
}

func checkResourceProtocol(node p.Node, options p.EvaluationContext) error {
	if len(node.Resources) == 0 {
		return nil
	}
	target := options.Identity()
	origin := node.Source
	if origin == nil && node.Native != nil {
		origin = &node.Native.Source
	}
	if origin == nil || origin.Protocol.Family != target.Family || origin.Protocol.WireVersion != target.WireVersion {
		return unsupported("/resources", "native signatures and references require their original wire protocol as well as account scope")
	}
	return nil
}

func (adapter module) encodeMedia(node p.Node, fields p.Object) error {
	media, err := node.Payload.ReadObject()
	if err != nil {
		return err
	}
	if node.Kind == p.ImageNode {
		switch adapter.name {
		case Chat, Responses:
			url, err := mediaURL(media)
			if err != nil {
				return err
			}
			fields["type"] = p.StringValue("input_image")
			fields["image_url"], fields["detail"] = url, media["detail"]
			if adapter.name == Chat {
				fields["type"] = p.StringValue("image_url")
				fields["image_url"] = object(p.Object{"url": url, "detail": media["detail"]})
				delete(fields, "detail")
			}
		case Anthropic:
			if !media["detail"].IsZero() {
				return unsupported("/image/detail", "Anthropic has no equivalent image detail option")
			}
			source, err := anthropicMediaSource(media)
			if err != nil {
				return err
			}
			fields["type"], fields["source"] = p.StringValue("image"), source
		}
		return nil
	}
	if node.Kind == p.AudioNode && adapter.name == Chat {
		format := media["format"]
		for name, mime := range audioFormats {
			if media["mime"] == p.StringValue(mime) {
				format = p.StringValue(name)
				break
			}
		}
		fields["type"], fields["input_audio"] = p.StringValue("input_audio"), object(p.Object{"data": media["data"], "format": format})
		if media["data"].IsZero() || format.IsZero() || !media["url"].IsZero() {
			return unsupported("/audio", "Chat audio requires encoded data and format; no automatic transcoding")
		}
		return nil
	}
	if node.Kind != p.DocumentNode {
		return unsupported("/media", "target has no equivalent media input")
	}
	switch adapter.name {
	case Chat:
		if !media["url"].IsZero() || !media["text"].IsZero() {
			return unsupported("/document", "Chat file data has no equivalent URL or plaintext source")
		}
		data, err := documentData(media)
		if err != nil {
			return err
		}
		fields["type"], fields["file"] = p.StringValue("file"), object(p.Object{"filename": media["name"], "file_data": data})
	case Responses:
		if !media["text"].IsZero() {
			return unsupported("/document", "Responses files require data or URL, not a plaintext document source")
		}
		data, err := documentData(media)
		if err != nil {
			return err
		}
		fields["type"], fields["file_data"], fields["file_url"], fields["filename"] = p.StringValue("input_file"), data, media["url"], media["name"]
	case Anthropic:
		if !media["name"].IsZero() {
			return unsupported("/document/name", "a filename cannot be substituted for an Anthropic document title")
		}
		source, err := anthropicMediaSource(media)
		if err != nil {
			return err
		}
		fields["type"], fields["source"] = p.StringValue("document"), source
	}
	return nil
}

func documentData(media p.Object) (p.Value, error) {
	if media["data"].IsZero() || media["mime"].IsZero() {
		return media["data"], nil
	}
	return mediaURL(media)
}

func mediaURL(media p.Object) (p.Value, error) {
	if !media["url"].IsZero() {
		return media["url"], nil
	}
	if media["data"].IsZero() {
		return p.Value{}, nil
	} // A separately scoped file reference may supply the source.
	data, err := stringValue(media["data"])
	if err != nil {
		return p.Value{}, err
	}
	mime, err := stringValue(media["mime"])
	if err != nil {
		return p.Value{}, err
	}
	return p.StringValue("data:" + mime + ";base64," + data), nil
}

func anthropicMediaSource(media p.Object) (p.Value, error) {
	if !media["text"].IsZero() {
		return object(p.Object{"type": p.StringValue("text"), "media_type": media["mime"], "data": media["text"]}), nil
	}
	if !media["data"].IsZero() {
		return object(p.Object{"type": p.StringValue("base64"), "media_type": media["mime"], "data": media["data"]}), nil
	}
	url, err := stringValue(media["url"])
	if err != nil {
		return p.Value{}, err
	}
	if !strings.HasPrefix(url, "data:") {
		return object(p.Object{"type": p.StringValue("url"), "url": media["url"]}), nil
	}
	meta, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return p.Value{}, unsupported("/media", "only explicitly base64 data URIs can be converted without transcoding")
	}
	return object(p.Object{"type": p.StringValue("base64"), "media_type": p.StringValue(strings.TrimSuffix(meta, ";base64")), "data": p.StringValue(data)}), nil
}

func (adapter module) encodeGeminiPart(node p.Node, fields p.Object) (p.Value, error) {
	if len(node.Cache) > 0 {
		return p.Value{}, unsupported("/cache", "Gemini uses explicit cache resources, not block breakpoints")
	}
	switch node.Kind {
	case p.TextNode, p.ReasoningNode:
		fields["text"] = node.Payload
		if node.Kind == p.ReasoningNode {
			if len(node.Children) > 0 {
				return p.Value{}, unsupported("/reasoning", "reasoning summary differs from visible thought")
			}
			flag, _ := p.EncodeValue(true)
			fields["thought"] = flag
		}
	case p.ToolCallNode:
		if node.Input.Kind != p.JSONInput {
			return p.Value{}, unsupported("/input", "Gemini requires JSON function input")
		}
		fields["functionCall"] = object(p.Object{"name": node.Name, "id": node.CallID, "args": node.Input.Value})
	case p.ToolResultNode:
		if node.Name.IsZero() {
			return p.Value{}, unsupported("/name", "Gemini results require the associated function name")
		}
		payload := node.Payload
		if !payload.IsObject() {
			// Chat 的工具结果内容是 JSON 字符串：可解析为对象时转换（Gemini
			// 标准 functionResponse.response 是对象）；不可解析保持显式拒绝。
			if text, err := stringValue(payload); err == nil {
				if parsed, parseErr := p.ParseValue([]byte(text)); parseErr == nil && parsed.IsObject() {
					payload = parsed
				}
			}
		}
		if !payload.IsObject() {
			return p.Value{}, unsupported("/payload", "Gemini function response requires an object; arbitrary text is not wrapped automatically")
		}
		fields["functionResponse"] = object(p.Object{"name": node.Name, "id": node.CallID, "response": payload})
	case p.ImageNode, p.AudioNode, p.VideoNode, p.DocumentNode:
		media, err := node.Payload.ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		if !media["detail"].IsZero() {
			return p.Value{}, unsupported("/detail", "Gemini has no equivalent image detail option")
		}
		if !media["data"].IsZero() {
			fields["inlineData"] = object(p.Object{"data": media["data"], "mimeType": media["mime"]})
		} else {
			fields["fileData"] = object(p.Object{"fileUri": media["url"], "mimeType": media["mime"]})
		}
		if media["mime"].IsZero() {
			return p.Value{}, unsupported("/media/mime", "Gemini requires an explicit media type")
		}
	default:
		return p.Value{}, unsupported("/content", "Gemini cannot express this content kind")
	}
	for _, resource := range node.Resources {
		if resource.Kind != "signature" {
			return p.Value{}, unsupported("/resources", fmt.Sprintf("Gemini part cannot express %s", resource.Kind))
		}
		fields["thoughtSignature"] = resource.ID
	}
	if err := adapter.preserveExtensions(fields, node.Attributes); err != nil {
		return p.Value{}, err
	}
	return object(fields), nil
}
