package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

type historyState struct {
	calls map[string][]p.Value
	next  int
}

func (adapter module) decodeContent(value p.Value, path string, direction p.Direction, options p.EvaluationContext, history *historyState) ([]p.Node, error) {
	if value.IsZero() {
		return nil, nil
	}
	if value.IsNull() {
		return []p.Node{{Kind: p.TextNode, Payload: value}}, nil
	}
	if text, err := stringValue(value); err == nil {
		return []p.Node{{Kind: p.TextNode, Payload: p.StringValue(text)}}, nil
	}
	items, err := readArray(value)
	if err != nil {
		return nil, err
	}
	nodes := make([]p.Node, 0, len(items))
	for index, item := range items {
		node, err := adapter.decodeBlock(item, fmt.Sprintf("%s/%d", path, index), direction, options, history)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (adapter module) decodeBlock(value p.Value, path string, direction p.Direction, options p.EvaluationContext, history *historyState) (p.Node, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return p.Node{}, err
	}
	cache, err := decodeCache(fields, "block")
	if err != nil {
		return p.Node{}, err
	}
	node := p.Node{Kind: p.OpaqueNode, Native: adapter.native(value, path, direction, options), Cache: cache}
	if adapter.name == Gemini {
		return adapter.decodeGeminiPart(fields, node, options, history)
	}
	kind, err := optionalString(fields["type"])
	if err != nil {
		return node, err
	}
	known := []string{"type", "cache_control"}
	nested := map[string][]string{}
	switch kind {
	case "text", "input_text", "output_text":
		node.Kind, node.Payload = p.TextNode, fields["text"]
		known = append(known, "text")
	case "refusal":
		node.Kind, node.Payload = p.RefusalNode, fields["refusal"]
		known = append(known, "refusal")
	case "thinking", "reasoning_text":
		textKey := "thinking"
		if kind == "reasoning_text" {
			textKey = "text"
		}
		node.Kind, node.Payload = p.ReasoningNode, fields[textKey]
		known = append(known, textKey, "signature")
		if signature := fields["signature"]; !signature.IsZero() {
			node.Resources = append(node.Resources, p.Resource{Kind: "signature", ID: signature, Scope: options.Scope})
		}
	case "redacted_thinking":
		node.Kind = p.ReasoningNode
		node.Resources = []p.Resource{{Kind: "encrypted_content", ID: fields["data"], Scope: options.Scope}}
		known = append(known, "data")
	case "tool_use", "function_call", "custom_tool_call":
		node, err = adapter.decodeCall(fields, node, kind)
		known = append(known, "id", "name")
		if kind != "tool_use" {
			known = append(known, "call_id", "status")
		}
		if kind == "function_call" {
			known = append(known, "arguments")
		} else {
			known = append(known, "input")
		}
	case "tool_result", "function_call_output", "custom_tool_call_output":
		node.Kind, node.CallID, node.Payload = p.ToolResultNode, fields["call_id"], fields["output"]
		if kind == "tool_result" {
			node.CallID, node.Payload = fields["tool_use_id"], fields["content"]
			known = append(known, "tool_use_id", "content", "is_error")
		} else {
			node.ID = fields["id"]
			known = append(known, "call_id", "output", "id")
		}
		if kind == "tool_result" && !fields["is_error"].IsZero() {
			node.Status = object(p.Object{"isError": fields["is_error"]})
		}
		if kind == "tool_result" && !node.Payload.IsNull() && strings.HasPrefix(strings.TrimSpace(string(node.Payload.Bytes())), "[") {
			node.Children, err = adapter.decodeContent(node.Payload, path+"/content", direction, options, history)
		}
	case "image_url", "input_image", "image":
		node.Kind = p.ImageNode
		node.Payload, err = decodeMedia(fields, kind)
		known = append(known, "file_id")
		switch kind {
		case "image_url":
			known = append(known, "image_url")
			nested["image_url"] = []string{"url", "detail"}
		case "input_image":
			known = append(known, "image_url", "detail")
		case "image":
			known = append(known, "source")
			nested["source"] = mediaSourceKeys(fields["source"])
		}
		if !fields["file_id"].IsZero() {
			node.Resources = []p.Resource{{Kind: "file_id", ID: fields["file_id"], Scope: options.Scope}}
		}
	case "input_audio":
		node.Kind = p.AudioNode
		audio, readErr := fields["input_audio"].ReadObject()
		if readErr != nil {
			return node, readErr
		}
		format, err := stringValue(audio["format"])
		if err != nil {
			return node, err
		}
		payload := p.Object{"data": audio["data"], "format": audio["format"]}
		if mime, exists := audioFormats[format]; exists {
			delete(payload, "format")
			payload["mime"] = p.StringValue(mime)
		}
		node.Payload = object(payload)
		known = append(known, "input_audio")
		nested["input_audio"] = []string{"data", "format"}
	case "file", "input_file", "document":
		node.Kind = p.DocumentNode
		media := fields
		if kind == "file" {
			media, err = fields["file"].ReadObject()
			if err != nil {
				return node, err
			}
		}
		if kind == "document" {
			node.Payload, err = decodeMedia(fields, kind)
		} else {
			payload := p.Object{"data": media["file_data"], "url": media["file_url"], "name": media["filename"]}
			if value := media["file_data"]; !value.IsZero() {
				decoded, err := mediaFromURL(value, p.Value{})
				if err != nil {
					return node, err
				}
				decodedFields, _ := decoded.ReadObject()
				if !decodedFields["mime"].IsZero() {
					payload["data"], payload["mime"] = decodedFields["data"], decodedFields["mime"]
				}
			}
			node.Payload = object(payload)
		}
		if !media["file_id"].IsZero() {
			node.Resources = []p.Resource{{Kind: "file_id", ID: media["file_id"], Scope: options.Scope}}
		}
		switch kind {
		case "file":
			known = append(known, "file")
			nested["file"] = []string{"file_data", "file_url", "file_id", "filename"}
		case "input_file":
			known = append(known, "file_data", "file_url", "file_id", "filename")
		case "document":
			known = append(known, "source")
			nested["source"] = mediaSourceKeys(fields["source"])
		}
	default:
		return node, nil
	}
	if err != nil {
		return node, err
	}
	node.Attributes, err = adapter.nestedExtensions(fields, known, nested)
	if err != nil {
		return node, err
	}
	if kind == "custom_tool_call_output" {
		if node.Attributes == nil {
			node.Attributes = p.Object{}
		}
		node.Attributes["inputKind"] = p.StringValue(string(p.TextInput))
	}
	return node, nil
}

func mediaSourceKeys(value p.Value) []string {
	fields, _ := value.ReadObject() // decodeMedia has already validated this object.
	if fields["type"] == p.StringValue("url") {
		return []string{"type", "url"}
	}
	return []string{"type", "data", "media_type"}
}

func (adapter module) decodeCall(fields p.Object, node p.Node, kind string) (p.Node, error) {
	node.Kind, node.ID, node.Name, node.CallID, node.Status = p.ToolCallNode, fields["id"], fields["name"], fields["call_id"], fields["status"]
	if kind == "tool_use" {
		node.CallID = fields["id"]
		node.ID = p.Value{}
		node.Status = p.Value{}
		node.Input = &p.ToolInput{Kind: p.JSONInput, Value: fields["input"]}
		return node, nil
	}
	if kind == "custom_tool_call" {
		node.Input = &p.ToolInput{Kind: p.TextInput, Value: fields["input"]}
		return node, nil
	}
	arguments, err := readJSONArguments(fields["arguments"])
	if err != nil {
		return node, fmt.Errorf("tool arguments: %w", err)
	}
	node.Input = &p.ToolInput{Kind: p.JSONInput, Value: arguments}
	return node, nil
}

func decodeMedia(fields p.Object, kind string) (p.Value, error) {
	switch kind {
	case "image_url":
		image, err := fields["image_url"].ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		return mediaFromURL(image["url"], image["detail"])
	case "input_image":
		return mediaFromURL(fields["image_url"], fields["detail"])
	default:
		source, err := fields["source"].ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		typeName, err := stringValue(source["type"])
		if err != nil {
			return p.Value{}, err
		}
		switch typeName {
		case "url":
			return object(p.Object{"url": source["url"]}), nil
		case "base64":
			return object(p.Object{"data": source["data"], "mime": source["media_type"]}), nil
		case "text":
			return object(p.Object{"text": source["data"], "mime": source["media_type"]}), nil
		default:
			return p.Value{}, unsupported("/source/type", "unsupported media source "+typeName)
		}
	}
}

// A base64 data URL and a base64 media object carry the same bytes. Normalize
// their container spelling without decoding, recompressing or transcoding data.
func mediaFromURL(value, detail p.Value) (p.Value, error) {
	if value.IsZero() {
		return object(p.Object{"detail": detail}), nil
	}
	url, err := stringValue(value)
	if err != nil {
		return p.Value{}, err
	}
	if strings.HasPrefix(url, "data:") {
		meta, data, exists := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
		if exists && strings.HasSuffix(meta, ";base64") {
			mime := strings.TrimSuffix(meta, ";base64")
			if mime != "" {
				return object(p.Object{"mime": p.StringValue(mime), "data": p.StringValue(data), "detail": detail}), nil
			}
		}
	}
	return object(p.Object{"url": value, "detail": detail}), nil
}

func (adapter module) decodeGeminiPart(fields p.Object, node p.Node, options p.EvaluationContext, history *historyState) (p.Node, error) {
	known := []string{"thoughtSignature"}
	nested := map[string][]string{}
	if signature := fields["thoughtSignature"]; !signature.IsZero() {
		node.Resources = append(node.Resources, p.Resource{Kind: "signature", ID: signature, Scope: options.Scope})
	}
	switch {
	case !fields["text"].IsZero():
		node.Kind, node.Payload = p.TextNode, fields["text"]
		if thought := fields["thought"]; !thought.IsZero() {
			var isThought bool
			if err := thought.Decode(&isThought); err != nil {
				return node, err
			}
			if isThought {
				node.Kind = p.ReasoningNode
			}
		}
		known = append(known, "text", "thought")
	case !fields["functionCall"].IsZero():
		call, err := fields["functionCall"].ReadObject()
		if err != nil {
			return node, err
		}
		node.Kind, node.Name, node.CallID = p.ToolCallNode, call["name"], call["id"]
		if node.CallID.IsZero() {
			node.CallID = p.StringValue(fmt.Sprintf("gemini_call_%d", history.next))
			history.next++
		}
		name, err := stringValue(node.Name)
		if err != nil {
			return node, err
		}
		history.calls[name] = append(history.calls[name], node.CallID)
		node.Input = &p.ToolInput{Kind: p.JSONInput, Value: call["args"]}
		known = append(known, "functionCall")
		nested["functionCall"] = []string{"id", "name", "args"}
	case !fields["functionResponse"].IsZero():
		result, err := fields["functionResponse"].ReadObject()
		if err != nil {
			return node, err
		}
		node.Kind, node.Name, node.CallID, node.Payload = p.ToolResultNode, result["name"], result["id"], result["response"]
		name, err := stringValue(node.Name)
		if err != nil {
			return node, err
		}
		if node.CallID.IsZero() {
			candidates := history.calls[name]
			if len(candidates) != 1 {
				return node, unsupported("/functionResponse", "result without id cannot be associated unambiguously")
			}
			node.CallID = candidates[0]
			delete(history.calls, name)
		}
		known = append(known, "functionResponse")
		nested["functionResponse"] = []string{"id", "name", "response"}
	case !fields["inlineData"].IsZero(), !fields["fileData"].IsZero():
		entry, isFile := fields["inlineData"], false
		if entry.IsZero() {
			entry, isFile = fields["fileData"], true
		}
		media, err := entry.ReadObject()
		if err != nil {
			return node, err
		}
		mime, err := stringValue(media["mimeType"])
		if err != nil {
			return node, err
		}
		node.Kind = p.DocumentNode
		for prefix, kind := range map[string]p.NodeKind{"image/": p.ImageNode, "audio/": p.AudioNode, "video/": p.VideoNode} {
			if strings.HasPrefix(mime, prefix) {
				node.Kind = kind
			}
		}
		payload := p.Object{"mime": media["mimeType"], "data": media["data"]}
		if isFile {
			payload = p.Object{"mime": media["mimeType"], "url": media["fileUri"]}
		}
		node.Payload = object(payload)
		if isFile {
			known = append(known, "fileData")
			nested["fileData"] = []string{"mimeType", "fileUri"}
		} else {
			known = append(known, "inlineData")
			nested["inlineData"] = []string{"mimeType", "data"}
		}
	default:
		return node, nil
	}
	var err error
	node.Attributes, err = adapter.nestedExtensions(fields, known, nested)
	return node, err
}
