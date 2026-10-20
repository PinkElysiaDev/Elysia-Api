package builtin

import (
	"context"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

// The construction engine is general; initial adapter rollout deliberately
// targets large Anthropic history without message-level overlays. Complex
// metadata/cache/native extensions and other codecs retain the established
// renderer. No probe execution or error-triggered rerun selects the path.
func (adapter module) canDeferRequest(request *p.Request) bool {
	if adapter.name != Anthropic {
		return false
	}
	for key := range request.Parameters {
		if strings.HasPrefix(key, wireExtensionPrefix) {
			return false
		}
	}
	large := false
	var hasLarge func([]p.Node)
	hasLarge = func(nodes []p.Node) {
		for _, node := range nodes {
			large = large || node.Payload.Len() >= 64<<10
			hasLarge(node.Children)
		}
	}
	for _, node := range request.Content {
		if node.Kind != p.MessageNode || (!node.Role.IsString("user") && !node.Role.IsString("assistant")) || hasItemMetadata(node) || len(node.Metadata) != 0 || len(node.Attributes) != 0 {
			return false
		}
		hasLarge(node.Children)
	}
	return large
}

func plannedObject(fields p.Object, nested map[string]p.JSONOutput) p.JSONOutput {
	output := make(map[string]p.JSONOutput, len(fields)+len(nested))
	for key, value := range fields {
		if !value.IsZero() {
			output[key] = p.JSONLeaf(value)
		}
	}
	for key, value := range nested {
		if !value.IsZero() {
			output[key] = value
		}
	}
	return p.JSONObject(output)
}

func (adapter module) planAnthropicMessages(ctx context.Context, nodes []p.Node, options p.EvaluationContext) (p.JSONOutput, error) {
	messages := make([]p.JSONOutput, 0, len(nodes))
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return p.JSONOutput{}, err
		}
		parts := make([]p.JSONOutput, 0, len(node.Children))
		for _, child := range node.Children {
			block, err := adapter.planAnthropicBlock(ctx, child, options)
			if err != nil {
				return p.JSONOutput{}, err
			}
			parts = append(parts, block)
		}
		messages = append(messages, plannedObject(p.Object{"role": node.Role}, map[string]p.JSONOutput{"content": p.JSONArray(parts)}))
	}
	return p.JSONArray(messages), nil
}

func (adapter module) planAnthropicBlock(ctx context.Context, node p.Node, options p.EvaluationContext) (p.JSONOutput, error) {
	if err := ctx.Err(); err != nil {
		return p.JSONOutput{}, err
	}
	if hasItemMetadata(node) || len(node.Metadata) != 0 || len(node.Attributes) != 0 || node.ReasoningForm != "" || node.ReasoningContent != nil {
		value, err := adapter.encodeBlock(node, p.EncodeRequest, options)
		return p.JSONLeaf(value), err
	}
	switch node.Kind {
	case p.TextNode:
		return plannedObject(p.Object{"type": p.StringValue("text"), "text": node.Payload}, nil), nil
	case p.ToolResultNode:
		if node.Payload.IsObject() {
			return p.JSONOutput{}, unsupported("/content/result", "object tool result requires tool_result_text conversion")
		}
		payload := p.JSONLeaf(node.Payload)
		if node.Children != nil {
			parts := make([]p.JSONOutput, 0, len(node.Children))
			for _, child := range node.Children {
				part, err := adapter.planAnthropicBlock(ctx, child, options)
				if err != nil {
					return p.JSONOutput{}, err
				}
				parts = append(parts, part)
			}
			payload = p.JSONArray(parts)
		}
		return plannedObject(p.Object{"type": p.StringValue("tool_result"), "tool_use_id": node.CallID}, map[string]p.JSONOutput{"content": payload}), nil
	case p.ImageNode, p.DocumentNode:
		media, err := node.Payload.ReadObject()
		if err != nil {
			return p.JSONOutput{}, err
		}
		kind := "image"
		if node.Kind == p.ImageNode && !media["detail"].IsZero() {
			return p.JSONOutput{}, unsupported("/image/detail", "Anthropic has no equivalent image detail option")
		}
		if node.Kind == p.DocumentNode {
			kind = "document"
			if !media["name"].IsZero() {
				return p.JSONOutput{}, unsupported("/document/name", "a filename cannot be substituted for an Anthropic document title")
			}
		}
		source, err := planAnthropicSource(media)
		if err != nil {
			return p.JSONOutput{}, err
		}
		return plannedObject(p.Object{"type": p.StringValue(kind)}, map[string]p.JSONOutput{"source": source}), nil
	default:
		value, err := adapter.encodeBlock(node, p.EncodeRequest, options)
		return p.JSONLeaf(value), err
	}
}

func planAnthropicSource(media p.Object) (p.JSONOutput, error) {
	if !media["text"].IsZero() {
		return plannedObject(p.Object{"type": p.StringValue("text"), "media_type": media["mime"], "data": media["text"]}, nil), nil
	}
	if !media["data"].IsZero() {
		return plannedObject(p.Object{"type": p.StringValue("base64"), "media_type": media["mime"], "data": media["data"]}, nil), nil
	}
	url, err := stringValue(media["url"])
	if err != nil {
		return p.JSONOutput{}, err
	}
	if !strings.HasPrefix(url, "data:") {
		return plannedObject(p.Object{"type": p.StringValue("url"), "url": media["url"]}, nil), nil
	}
	meta, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return p.JSONOutput{}, unsupported("/media", "only explicitly base64 data URIs can be converted without transcoding")
	}
	return plannedObject(p.Object{"type": p.StringValue("base64"), "media_type": p.StringValue(strings.TrimSuffix(meta, ";base64")), "data": p.StringValue(data)}, nil), nil
}
