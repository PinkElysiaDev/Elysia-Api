package relay

import (
	"sort"
	"strings"
)

// Separate Anthropic-style directives from Gemini resource names carried by the
// legacy CacheControl field. Cross-wire conversion must not confuse the two.
func cacheControlObject(value any) any {
	if object, ok := value.(map[string]any); ok && len(object) > 0 {
		return object
	}
	return nil
}

func cachedContentReference(value any) string {
	text, _ := value.(string)
	return text
}

func contentPartCacheControl(part MaheshvaraContentPart) any {
	if value := cacheControlObject(part.CacheControl); value != nil {
		return value
	}
	return cacheControlObject(rawBlockCacheControl(part.Raw))
}

func toolCallCacheControl(call MaheshvaraToolCall) any {
	if value := cacheControlObject(call.CacheControl); value != nil {
		return value
	}
	return cacheControlObject(rawBlockCacheControl(call.Raw))
}

func withCacheControl(block map[string]any, control any) map[string]any {
	if block != nil {
		if value := cacheControlObject(control); value != nil {
			block["cache_control"] = value
		}
	}
	return block
}

// Keep native system blocks and cross-wire breakpoints. Unmarked requests retain
// the established string representation for compatibility.
func maheshvaraAnthropicSystem(req *MaheshvaraRequest) any {
	if raw := req.RawExtra["claude_system_blocks"]; len(raw) > 0 {
		return jsonRawToAny(raw)
	}
	var blocks []map[string]any
	marked := false
	if strings.TrimSpace(req.Instructions) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": req.Instructions})
	}
	for _, msg := range req.Messages {
		if _, system := normalizeMaheshvaraRole(msg.Role); !system {
			continue
		}
		start := len(blocks)
		for _, part := range msg.Content {
			if part.Type != MaheshvaraContentText || part.Text == "" {
				continue
			}
			cc := contentPartCacheControl(part)
			marked = marked || cc != nil
			blocks = append(blocks, withCacheControl(map[string]any{"type": "text", "text": part.Text}, cc))
		}
		if cc := cacheControlObject(msg.CacheControl); cc != nil && len(blocks) > start {
			last := blocks[len(blocks)-1]
			if last["cache_control"] == nil {
				last["cache_control"] = cc
			}
			marked = true
		}
	}
	if marked {
		return blocks
	}
	if text := maheshvaraResponsesInstructions(req); text != "" {
		return text
	}
	return nil
}

func restoreClaudeBlockOrder(blocks []map[string]any, indexes []*int) {
	if len(blocks) != len(indexes) {
		return
	}
	type positionedBlock struct {
		index int
		block map[string]any
	}
	ordered := make([]positionedBlock, len(blocks))
	for i, index := range indexes {
		// Cross-wire/generated content uses the regular converter order.
		if index == nil {
			return
		}
		ordered[i] = positionedBlock{*index, blocks[i]}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	for i, item := range ordered {
		blocks[i] = item.block
	}
}
