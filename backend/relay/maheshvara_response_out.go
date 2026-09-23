// 响应双向整形：上游线制响应 → Maheshvara 核心响应 → 调用方线制响应（含停止原因映射）。
package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OpenAIChatResponseToMaheshvara 把 Chat 响应转换为核心响应。
func OpenAIChatResponseToMaheshvara(resp *OpenAIResponse) (*MaheshvaraResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil OpenAI response")
	}
	out := &MaheshvaraResponse{
		ID:                resp.ID,
		Model:             resp.Model,
		CreatedAt:         resp.Created,
		Status:            "completed",
		SystemFingerprint: resp.SystemFingerprint,
		Usage:             maheshvaraUsageFromOpenAIUsage(resp.Usage),
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		item := MaheshvaraOutputItem{
			ID:      newMaheshvaraResponseID("msg"),
			Type:    MaheshvaraOutputMessage,
			Status:  "completed",
			Role:    choice.Message.Role,
			Content: interfaceToContentParts(choice.Message.Content),
		}
		if item.Role == "" {
			item.Role = "assistant"
		}
		for _, part := range item.Content {
			if part.Type == MaheshvaraContentReasoning {
				out.Output = append(out.Output, MaheshvaraOutputItem{
					ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
					Content: []MaheshvaraContentPart{part},
				})
			}
		}
		if choice.Message.ReasoningContent != "" {
			out.Output = append(out.Output, MaheshvaraOutputItem{
				ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
				Content: []MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: choice.Message.ReasoningContent, ReasoningText: choice.Message.ReasoningContent}},
			})
		}
		// OpenRouter 风格推理明细：每条一 item（不合并不去重），密文带签发方。
		for _, part := range openAIReasoningDetailsToParts(choice.Message.ReasoningDetails) {
			out.Output = append(out.Output, MaheshvaraOutputItem{
				ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
				Content: []MaheshvaraContentPart{part},
			})
		}
		if choice.Message.Refusal != "" {
			item.Content = append(item.Content, MaheshvaraContentPart{Type: MaheshvaraContentRefusal, Text: choice.Message.Refusal})
		}
		if choice.Message.Audio != nil {
			if audioPart := openAIAudioValueToPart(choice.Message.Audio); audioPart != nil {
				item.Content = append(item.Content, *audioPart)
			}
		}
		out.Output = append(out.Output, item)
		for _, call := range choice.Message.ToolCalls {
			thoughtSignature := ""
			thoughtSignatureProvider := ""
			if call.ExtraContent != nil && call.ExtraContent.Google != nil {
				thoughtSignature = call.ExtraContent.Google.ThoughtSignature
				if thoughtSignature != "" {
					thoughtSignatureProvider = MaheshvaraSignatureProviderGemini
				}
			}
			out.Output = append(out.Output, MaheshvaraOutputItem{
				ID:        call.ID,
				Type:      MaheshvaraOutputFunctionCall,
				Status:    "completed",
				CallID:    call.ID,
				Name:      call.Function.Name,
				Arguments: json.RawMessage(call.Function.Arguments),
				ToolCalls: []MaheshvaraToolCall{{ID: call.ID, Type: MaheshvaraToolFunction, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments), ThoughtSignature: thoughtSignature, ThoughtSignatureProvider: thoughtSignatureProvider}},
			})
		}
		out.StopReason = choice.FinishReason
	}
	return out, nil
}

// AnthropicResponseToMaheshvara 把 Claude 响应转换为核心响应。
func AnthropicResponseToMaheshvara(resp *ClaudeResponse) (*MaheshvaraResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Claude response")
	}
	out := &MaheshvaraResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		CreatedAt:  time.Now().Unix(),
		Status:     "completed",
		StopReason: resp.StopReason,
		Usage:      maheshvaraUsageFromClaudeUsage(resp.Usage),
	}
	msg := MaheshvaraOutputItem{ID: resp.ID, Type: MaheshvaraOutputMessage, Status: "completed", Role: "assistant"}
	// 按 block 原始出现顺序输出：遇到 thinking/tool_use 等独立 item 前先冲刷
	// 已累积的 message。Anthropic 要求启用 thinking 时 thinking 块必须是 assistant
	// 消息的第一块；若把 msg 一律前插，[thinking, text] 会变成 [text, thinking]，
	// Claude→maheshvara→Claude 往返即违反约束。
	flushMsg := func() {
		if len(msg.Content) > 0 {
			out.Output = append(out.Output, msg)
			msg = MaheshvaraOutputItem{ID: resp.ID, Type: MaheshvaraOutputMessage, Status: "completed", Role: "assistant"}
		}
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentText, Text: block.Text, Citations: block.Citations, Raw: block})
		case "thinking":
			flushMsg()
			part := MaheshvaraContentPart{Type: MaheshvaraContentReasoning, ReasoningText: block.Thinking, Text: block.Thinking, Signature: block.Signature, SignatureProvider: MaheshvaraSignatureProviderAnthropic}
			if envelope, ok := decodeMaheshvaraReasoningEnvelope(block.Signature); ok {
				part.Signature = ""
				part.SignatureProvider = MaheshvaraSignatureProviderMaheshvara
				part.EncryptedContent = envelope.EncryptedContent
				part.EncryptedProvider = envelope.Provider
				part.EncryptedModel = envelope.Model
				part.ReasoningSummary = envelope.Summary
				if part.Text == "" {
					part.Text = envelope.Text
					part.ReasoningText = envelope.Text
				}
			}
			out.Output = append(out.Output, MaheshvaraOutputItem{
				ID:      newMaheshvaraResponseID("rs"),
				Type:    MaheshvaraOutputReasoning,
				Status:  "completed",
				Content: []MaheshvaraContentPart{part},
			})
		case "redacted_thinking":
			flushMsg()
			if envelope, ok := decodeMaheshvaraReasoningEnvelope(block.Data); ok {
				out.Output = append(out.Output, MaheshvaraOutputItem{
					ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
					Content: []MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: envelope.Text, ReasoningText: envelope.Text, SignatureProvider: MaheshvaraSignatureProviderMaheshvara, EncryptedContent: envelope.EncryptedContent, EncryptedProvider: envelope.Provider, EncryptedModel: envelope.Model, ReasoningSummary: envelope.Summary}},
				})
			}
		case "tool_use":
			flushMsg()
			out.Output = append(out.Output, MaheshvaraOutputItem{
				ID:        block.ID,
				Type:      MaheshvaraOutputFunctionCall,
				Status:    "completed",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		case "image":
			msg.Content = append(msg.Content, claudeImageBlockToPart(map[string]any{"type": "image", "source": block.Source}))
		case "document", "file":
			msg.Content = append(msg.Content, claudeDocumentBlockToPart(map[string]any{"type": block.Type, "source": block.Source}))
		case "tool_result":
			msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentToolOutput, ToolCallID: block.ToolUseID, ToolOutput: customValueString(block.Content), Raw: block})
		default:
			// server_tool_use / web_search_tool_result 等服务端工具块与未知
			// 块：整块原样保留（RawFields 捕获了全部字段），Claude 目标渲染
			// 时整块回放；跨协议目标按未知 part 处理。
			if block.Type != "" {
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: block.Type, Raw: block.RawFields})
			}
		}
	}
	flushMsg()
	return out, nil
}

// GeminiResponseToMaheshvara 把 Gemini 响应转换为核心响应。
func GeminiResponseToMaheshvara(resp *GeminiResponse) (*MaheshvaraResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Gemini response")
	}
	out := &MaheshvaraResponse{
		ID:        resp.ResponseID,
		Model:     resp.ModelVersion,
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Usage:     maheshvaraUsageFromGeminiUsage(resp.UsageMetadata),
	}
	if out.ID == "" {
		out.ID = newMaheshvaraResponseID("gemini")
	}
	msg := MaheshvaraOutputItem{ID: newMaheshvaraResponseID("msg"), Type: MaheshvaraOutputMessage, Status: "completed", Role: "assistant"}
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		out.StopReason = cand.FinishReason
		// 搜索/据实来源标注挂在首个文本 part 的 annotations 上（包装原始
		// JSON），Gemini 目标渲染时提取回 candidate.groundingMetadata。
		groundingAttached := false
		attachGrounding := func(part *MaheshvaraContentPart) {
			if groundingAttached || len(cand.GroundingMetadata) == 0 {
				return
			}
			groundingAttached = true
			var metadata any
			if err := json.Unmarshal(cand.GroundingMetadata, &metadata); err == nil {
				if annotation, ok := metadata.(map[string]any); ok {
					part.Annotations = append(part.Annotations, map[string]any{MaheshvaraAnnotationGeminiGrounding: annotation})
				}
			}
		}
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					out.Output = append(out.Output, MaheshvaraOutputItem{
						ID: newMaheshvaraResponseID("rs"), Type: MaheshvaraOutputReasoning, Status: "completed",
						Content: []MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: part.Text, ReasoningText: part.Text, Signature: part.ThoughtSignature, SignatureProvider: MaheshvaraSignatureProviderGemini}},
					})
				} else {
					textPart := MaheshvaraContentPart{Type: MaheshvaraContentText, Text: part.Text}
					attachGrounding(&textPart)
					msg.Content = append(msg.Content, textPart)
				}
			}
			if part.FunctionCall != nil {
				functionCall, _ := part.FunctionCall.(map[string]any)
				raw, _ := json.Marshal(functionCall["args"])
				callID := stringValue(functionCall["id"])
				name := stringValue(functionCall["name"])
				out.Output = append(out.Output, MaheshvaraOutputItem{
					ID:        newMaheshvaraResponseID("call"),
					Type:      MaheshvaraOutputFunctionCall,
					Status:    "completed",
					CallID:    callID,
					Name:      name,
					Arguments: raw,
					ToolCalls: []MaheshvaraToolCall{{ID: callID, Type: MaheshvaraToolFunction, Name: name, Arguments: raw, ThoughtSignature: part.ThoughtSignature, ThoughtSignatureProvider: MaheshvaraSignatureProviderGemini}},
				})
			}
			if part.InlineData != nil {
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentImage, MediaType: firstNonEmptyString(stringValue(part.InlineData["mimeType"]), stringValue(part.InlineData["mime_type"])), ImageBase64: stringValue(part.InlineData["data"])})
			}
			if part.FileData != nil {
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentFile, MediaType: firstNonEmptyString(stringValue(part.FileData["mimeType"]), stringValue(part.FileData["mime_type"])), URI: firstNonEmptyString(stringValue(part.FileData["fileUri"]), stringValue(part.FileData["file_uri"]))})
			}
		}
	}
	if len(msg.Content) > 0 {
		out.Output = append([]MaheshvaraOutputItem{msg}, out.Output...)
	}
	return out, nil
}

// OpenAIResponsesResponseToMaheshvara 把 Responses 响应转换为核心
// 响应;输出含 function_call 时置 StopReason=tool_calls。
func OpenAIResponsesResponseToMaheshvara(resp *OpenAIResponsesResponse) (*MaheshvaraResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Responses response")
	}
	out := &MaheshvaraResponse{
		ID:                resp.ID,
		Model:             resp.Model,
		CreatedAt:         resp.CreatedAt,
		Status:            resp.Status,
		Usage:             maheshvaraUsageFromResponsesUsage(resp.Usage),
		IncompleteDetails: resp.IncompleteDetails,
		Metadata:          resp.Metadata,
		ServiceTier:       resp.ServiceTier,
	}
	// 输出含 function_call 即工具轮:置 StopReason=tool_calls,否则经
	// maheshvaraStopTo* 塌缩成 stop/end_turn,依赖 finish 信号的客户端漏调度。
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			out.StopReason = "tool_calls"
			break
		}
	}
	if resp.Error != nil {
		if object := mapValue(resp.Error); object != nil {
			out.Error = &MaheshvaraError{
				Message: stringValue(object["message"]),
				Type:    stringValue(object["type"]),
				Code:    stringValue(object["code"]),
				Param:   stringValue(object["param"]),
				Raw:     object,
			}
		} else {
			out.Error = &MaheshvaraError{Message: contentValueToString(resp.Error)}
		}
	}
	for index, item := range resp.Output {
		var rawOutput map[string]any
		if index < len(resp.RawOutputs) {
			rawOutput = resp.RawOutputs[index]
		}
		out.Output = append(out.Output, responsesItemToMaheshvara(item, rawOutput, resp.Model))
		if out.Usage != nil {
			switch item.Type {
			case MaheshvaraOutputWebSearchCall:
				out.Usage.WebSearchCallCount++
			case MaheshvaraOutputFileSearchCall:
				out.Usage.FileSearchCallCount++
			case MaheshvaraOutputImageGenerationCall:
				out.Usage.ImageGenerationCallCount++
			}
		}
	}
	return out, nil
}

// responsesItemToMaheshvara 转换单个 Responses 输出项；rawOutput 为同位原始
// 对象（可能为 nil），服务端工具项（web_search_call 等）没有类型化载荷字段，
// 整项原始对象挂到 Raw，Responses 目标渲染时原样回放，不再只剩空壳。
func responsesItemToMaheshvara(item ResponsesOutput, rawOutput map[string]any, model string) MaheshvaraOutputItem {
	citem := MaheshvaraOutputItem{
		ID:        item.ID,
		Type:      item.Type,
		Status:    item.Status,
		Role:      item.Role,
		CallID:    item.CallID,
		Name:      item.Name,
		Arguments: item.Arguments,
		Raw:       map[string]any{"quality": item.Quality, "size": item.Size},
	}
	if rawOutput != nil {
		switch item.Type {
		case "message", "reasoning", "function_call", "custom_tool_call":
		default:
			citem.Raw = rawOutput
		}
	}
	for _, content := range item.Content {
		switch content.Type {
		case "output_text", "text":
			citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentText, Text: content.Text, Annotations: content.Annotations})
		case "refusal":
			citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentRefusal, Text: content.Refusal})
		case "input_image", "image":
			citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentImage, ImageURL: content.ImageURL})
		case "input_file", "file":
			citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentFile, FileID: content.FileID, URI: content.FileURL, FileName: content.Filename})
		case "input_audio", "audio":
			part := MaheshvaraContentPart{Type: MaheshvaraContentAudio, Raw: content.Audio}
			if content.Audio != nil {
				part.AudioBase64 = firstNonEmptyString(stringValue(content.Audio["data"]), stringValue(content.Audio["audio_data"]))
				part.AudioURL = firstNonEmptyString(stringValue(content.Audio["url"]), stringValue(content.Audio["audio_url"]))
				part.MediaType = firstNonEmptyString(stringValue(content.Audio["format"]), stringValue(content.Audio["mime_type"]))
			}
			citem.Content = append(citem.Content, part)
		}
	}
	for _, summary := range item.Summary {
		citem.Summary = append(citem.Summary, MaheshvaraReasoningSummary{Type: summary.Type, Text: summary.Text})
	}
	if item.Type == MaheshvaraOutputReasoning {
		var summaryText strings.Builder
		for _, summary := range citem.Summary {
			summaryText.WriteString(summary.Text)
		}
		citem.Reasoning = &MaheshvaraReasoning{
			Text:             summaryText.String(),
			Summary:          summaryText.String(),
			SummaryParts:     append([]MaheshvaraReasoningSummary(nil), citem.Summary...),
			EncryptedContent: item.EncryptedContent,
		}
		if summaryText.Len() > 0 || item.EncryptedContent != "" {
			citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentReasoning, Text: summaryText.String(), ReasoningText: summaryText.String(), EncryptedContent: item.EncryptedContent, EncryptedProvider: MaheshvaraSignatureProviderOpenAI, EncryptedModel: model, ReasoningSummary: citem.Summary})
		}
	}
	return citem
}

func MaheshvaraToOpenAIChatResponse(resp *MaheshvaraResponse) (*OpenAIResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Maheshvara response")
	}
	msg := Message{Role: "assistant", Content: ""}
	var toolCalls []OpenAIToolCall
	var messageParts []MaheshvaraContentPart
	for _, item := range resp.Output {
		switch item.Type {
		case MaheshvaraOutputMessage:
			for _, part := range item.Content {
				switch part.Type {
				case MaheshvaraContentReasoning:
					continue
				case MaheshvaraContentRefusal:
					if part.Text != "" {
						msg.Refusal += part.Text
					}
				case MaheshvaraContentAudio:
					if msg.Audio == nil {
						if part.Raw != nil {
							msg.Audio = part.Raw
						} else {
							msg.Audio = map[string]any{"data": firstNonEmptyString(part.AudioBase64, part.Data), "url": part.AudioURL, "transcript": part.Text}
						}
					}
				default:
					// 协议专属块（Claude server_tool_use 等）不得成为 OpenAI 消息
					// content part（严格 SDK 反序列化失败）——contentPartsToInterface
					// 的类型白名单会在渲染时丢弃它们，此处同样过滤。
					if raw, ok := part.Raw.(map[string]any); ok && !isOpenAIContentPartType(stringValue(raw["type"])) && part.Type != MaheshvaraContentText && part.Type != MaheshvaraContentImage && part.Type != MaheshvaraContentVideo && part.Type != MaheshvaraContentFile && part.Type != MaheshvaraContentDocument {
						continue
					}
					messageParts = append(messageParts, part)
				}
			}
		case MaheshvaraOutputFunctionCall:
			arguments := nonEmptyJSONArgs(strings.TrimSpace(string(item.Arguments)))
			toolCall := OpenAIToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: OpenAIToolFunction{
					Name:      item.Name,
					Arguments: arguments,
				},
			}
			if len(item.ToolCalls) > 0 {
				if signature := maheshvaraSignatureForProvider(item.ToolCalls[0].ThoughtSignature, item.ToolCalls[0].ThoughtSignatureProvider, MaheshvaraSignatureProviderGemini); signature != "" {
					toolCall.ExtraContent = &OpenAIToolCallExtraContent{Google: &OpenAIToolCallGoogleExtraContent{ThoughtSignature: signature}}
				}
			}
			toolCalls = append(toolCalls, toolCall)
		case MaheshvaraOutputReasoning:
			msg.ReasoningContent += maheshvaraReasoningText(item)
		}
	}
	if len(messageParts) == 1 && messageParts[0].Type == MaheshvaraContentText && len(messageParts[0].Annotations) == 0 {
		msg.Content = messageParts[0].Text
	} else if len(messageParts) > 0 {
		msg.Content = contentPartsToInterface(messageParts)
	}
	msg.ToolCalls = toolCalls
	return &OpenAIResponse{
		ID:                resp.ID,
		Object:            "chat.completion",
		Created:           resp.CreatedAt,
		Model:             resp.Model,
		SystemFingerprint: resp.SystemFingerprint,
		Choices:           []Choice{{Index: 0, Message: msg, FinishReason: maheshvaraStopToOpenAI(resp.StopReason)}},
		Usage:             openAIUsageFromMaheshvara(resp.Usage),
	}, nil
}

func MaheshvaraToAnthropicResponse(resp *MaheshvaraResponse) (*ClaudeResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Maheshvara response")
	}
	var content []ClaudeContent
	for _, item := range resp.Output {
		switch item.Type {
		case MaheshvaraOutputMessage:
			for _, part := range item.Content {
				blocks, err := claudeBlocksFromMessagePart(part, resp.Model)
				if err != nil {
					return nil, err
				}
				content = append(content, blocks...)
			}
		case MaheshvaraOutputReasoning:
			text := maheshvaraReasoningText(item)
			sigPart := MaheshvaraContentPart{}
			if len(item.Content) > 0 {
				sigPart = item.Content[0]
			}
			if item.Reasoning != nil && sigPart.EncryptedContent == "" {
				sigPart.EncryptedContent = item.Reasoning.EncryptedContent
			}
			signature := claudeThinkingSignatureForPart(sigPart, resp.Model)
			if text != "" || signature != "" {
				content = append(content, ClaudeContent{Type: "thinking", Thinking: text, Signature: signature})
			}
		case MaheshvaraOutputFunctionCall:
			if strings.TrimSpace(item.Name) == "" {
				return nil, fmt.Errorf("cannot convert Maheshvara response to Claude: function call is missing a name")
			}
			content = append(content, ClaudeContent{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: item.Arguments})
		}
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("cannot convert Maheshvara response to Claude: no representable output content")
	}
	return &ClaudeResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      resp.Model,
		StopReason: maheshvaraStopToClaude(resp.StopReason),
		Usage:      claudeUsageFromMaheshvara(resp.Usage),
	}, nil
}

// claudeContentFromMap 经 JSON 往返把 map 形态的块收敛为 ClaudeContent
// （与既有 appendMapContent 行为一致，数字经反序列化归一）。
func claudeContentFromMap(raw map[string]any) (ClaudeContent, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ClaudeContent{}, err
	}
	var block ClaudeContent
	if err := json.Unmarshal(encoded, &block); err != nil {
		return ClaudeContent{}, err
	}
	return block, nil
}

// claudeBlocksFromMessagePart 把单条消息 part 渲染为 0 或 1 个 Claude
// content 块；map 形态块走 JSON 往返保持数字归一行为。
func claudeBlocksFromMessagePart(part MaheshvaraContentPart, model string) ([]ClaudeContent, error) {
	switch part.Type {
	case MaheshvaraContentText:
		if part.Text != "" {
			return []ClaudeContent{{Type: "text", Text: part.Text, Citations: part.Citations}}, nil
		}
	case MaheshvaraContentReasoning:
		text := firstNonEmptyString(part.ReasoningText, part.Text)
		if signature := claudeThinkingSignatureForPart(part, model); text != "" || signature != "" {
			return []ClaudeContent{{Type: "thinking", Thinking: text, Signature: signature}}, nil
		}
	case MaheshvaraContentRefusal:
		if part.Text != "" {
			return []ClaudeContent{{Type: "text", Text: part.Text}}, nil
		}
	case MaheshvaraContentImage:
		if source := imagePartToClaudeSource(part); source != nil {
			block, err := claudeContentFromMap(map[string]any{"type": "image", "source": source})
			if err != nil {
				return nil, err
			}
			return []ClaudeContent{block}, nil
		}
	case MaheshvaraContentDocument, MaheshvaraContentFile:
		if block := maheshvaraDocumentToClaudeBlock(part); block != nil {
			converted, err := claudeContentFromMap(block)
			if err != nil {
				return nil, err
			}
			return []ClaudeContent{converted}, nil
		}
	case MaheshvaraContentAudio, MaheshvaraContentVideo:
		if block := maheshvaraMediaToClaudeBlock(part); block != nil {
			converted, err := claudeContentFromMap(block)
			if err != nil {
				return nil, err
			}
			return []ClaudeContent{converted}, nil
		}
	case MaheshvaraContentToolOutput:
		if part.ToolCallID != "" {
			return []ClaudeContent{{Type: "tool_result", ToolUseID: part.ToolCallID, Content: part.ToolOutput}}, nil
		}
	default:
		// 服务端工具块与未知 Claude 块：整块原样回放。
		if raw, ok := part.Raw.(map[string]any); ok {
			if _, hasType := raw["type"]; hasType {
				block, err := claudeContentFromMap(raw)
				if err != nil {
					return nil, err
				}
				return []ClaudeContent{block}, nil
			}
		}
	}
	return nil, nil
}

func MaheshvaraToGeminiResponse(resp *MaheshvaraResponse) (*GeminiResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Maheshvara response")
	}
	var parts []GeminiPart
	firstFunctionCallIndex := -1
	// 从文本 part 的 annotations 提取 Gemini 据实来源标注，渲染回 candidate。
	groundingMetadata := extractGeminiGroundingMetadata(resp)
	appendRawPart := func(raw map[string]any) error {
		if raw == nil {
			return nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		var part GeminiPart
		if err := json.Unmarshal(encoded, &part); err != nil {
			return err
		}
		parts = append(parts, part)
		return nil
	}
	for _, item := range resp.Output {
		switch item.Type {
		case MaheshvaraOutputMessage:
			for _, contentPart := range item.Content {
				switch contentPart.Type {
				case MaheshvaraContentText, MaheshvaraContentRefusal:
					if contentPart.Text != "" {
						parts = append(parts, GeminiPart{Text: contentPart.Text})
					}
				case MaheshvaraContentReasoning:
					text := firstNonEmptyString(contentPart.ReasoningText, contentPart.Text)
					if text != "" {
						part := GeminiPart{Text: text, Thought: true, ThoughtSignature: maheshvaraSignatureForProvider(contentPart.Signature, contentPart.SignatureProvider, MaheshvaraSignatureProviderGemini)}
						parts = append(parts, part)
					}
				default:
					if err := appendRawPart(maheshvaraPartToGeminiPart(contentPart)); err != nil {
						return nil, err
					}
				}
			}
		case MaheshvaraOutputFunctionCall:
			functionCall := map[string]any{"name": item.Name, "args": jsonRawToAny(item.Arguments)}
			if item.CallID != "" {
				functionCall["id"] = item.CallID
			}
			part := GeminiPart{FunctionCall: functionCall}
			if firstFunctionCallIndex < 0 {
				firstFunctionCallIndex = len(parts)
			}
			if len(item.ToolCalls) > 0 {
				part.ThoughtSignature = maheshvaraSignatureForProvider(item.ToolCalls[0].ThoughtSignature, item.ToolCalls[0].ThoughtSignatureProvider, MaheshvaraSignatureProviderGemini)
			}
			parts = append(parts, part)
		case MaheshvaraOutputReasoning:
			text := maheshvaraReasoningText(item)
			if text != "" {
				part := GeminiPart{Text: text, Thought: true}
				if len(item.Content) > 0 {
					part.ThoughtSignature = maheshvaraSignatureForProvider(item.Content[0].Signature, item.Content[0].SignatureProvider, MaheshvaraSignatureProviderGemini)
				}
				parts = append(parts, part)
			}
		}
	}
	if firstFunctionCallIndex >= 0 && parts[firstFunctionCallIndex].ThoughtSignature == "" {
		parts[firstFunctionCallIndex].ThoughtSignature = geminiCrossProviderThoughtSignature
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("cannot convert Maheshvara response to Gemini: no representable output part")
	}
	return &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content:           GeminiContent{Role: "model", Parts: parts},
			FinishReason:      maheshvaraStopToGemini(resp.StopReason),
			GroundingMetadata: groundingMetadata,
		}},
		UsageMetadata: geminiUsageFromMaheshvara(resp.Usage),
		ModelVersion:  resp.Model,
		ResponseID:    resp.ID,
	}, nil
}

// extractGeminiGroundingMetadata 从 maheshvara 输出的文本 part annotations 中
// 提取包装的 groundingMetadata 原始对象（首个命中即可，candidate 级字段）。
func extractGeminiGroundingMetadata(resp *MaheshvaraResponse) json.RawMessage {
	for _, item := range resp.Output {
		if item.Type != MaheshvaraOutputMessage {
			continue
		}
		for _, part := range item.Content {
			for _, annotation := range part.Annotations {
				if value, ok := annotation[MaheshvaraAnnotationGeminiGrounding]; ok {
					if encoded, err := json.Marshal(value); err == nil {
						return encoded
					}
				}
			}
		}
	}
	return nil
}

func MaheshvaraToOpenAIResponsesResponse(resp *MaheshvaraResponse) (*OpenAIResponsesResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Maheshvara response")
	}
	out := &OpenAIResponsesResponse{
		ID:                resp.ID,
		Object:            "response",
		CreatedAt:         resp.CreatedAt,
		Status:            resp.Status,
		Model:             resp.Model,
		Usage:             responsesUsageFromMaheshvara(resp.Usage),
		IncompleteDetails: resp.IncompleteDetails,
		Metadata:          resp.Metadata,
		ServiceTier:       resp.ServiceTier,
	}
	if out.ID == "" {
		out.ID = newMaheshvaraResponseID("resp")
	}
	if out.CreatedAt == 0 {
		out.CreatedAt = time.Now().Unix()
	}
	if out.Status == "" {
		out.Status = "completed"
	}
	if resp.Error != nil {
		out.Error = map[string]any{
			"type":    resp.Error.Type,
			"code":    resp.Error.Code,
			"param":   resp.Error.Param,
			"message": resp.Error.Message,
		}
	}
	for _, item := range resp.Output {
		ritem := ResponsesOutput{
			ID:        item.ID,
			Type:      item.Type,
			Status:    item.Status,
			Role:      item.Role,
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Metadata:  item.Metadata,
		}
		// 服务端工具项整项回放：原始对象为底，类型化字段覆盖其上
		//（仅当 Raw 是带 type 的完整原始项，而非 quality/size 摘要）。
		switch item.Type {
		case MaheshvaraOutputMessage, MaheshvaraOutputFunctionCall, MaheshvaraOutputReasoning:
		default:
			if _, hasType := item.Raw["type"]; hasType {
				ritem.RawItem = item.Raw
			}
		}
		if ritem.Type == MaheshvaraOutputMessage || ritem.Type == "message" {
			ritem.Type = "message"
			ritem.Role = "assistant"
			for _, part := range item.Content {
				if part.Type == MaheshvaraContentReasoning {
					continue
				}
				if rendered, ok := maheshvaraPartToResponsesOutputContent(part); ok {
					ritem.Content = append(ritem.Content, rendered)
				}
			}
		}
		if ritem.Type == MaheshvaraOutputFunctionCall {
			ritem.Type = "function_call"
		}
		if ritem.Type == MaheshvaraOutputReasoning {
			ritem.Type = "reasoning"
			for _, s := range maheshvaraReasoningSummary(item) {
				ritem.Summary = append(ritem.Summary, ResponsesReasoningSummaryPart{Type: s.Type, Text: s.Text})
			}
			ritem.EncryptedContent = maheshvaraReasoningEncryptedContent(item)
		}
		out.Output = append(out.Output, ritem)
	}
	return out, nil
}

func maheshvaraPartToResponsesOutputContent(part MaheshvaraContentPart) (ResponsesOutputContent, bool) {
	switch part.Type {
	case MaheshvaraContentText:
		return ResponsesOutputContent{Type: "output_text", Text: part.Text, Annotations: part.Annotations}, part.Text != ""
	case MaheshvaraContentRefusal:
		return ResponsesOutputContent{Type: "refusal", Refusal: part.Text, Annotations: part.Annotations}, part.Text != ""
	case MaheshvaraContentImage:
		return ResponsesOutputContent{Type: "image", ImageURL: firstNonEmptyString(part.ImageURL, part.URI)}, firstNonEmptyString(part.ImageURL, part.URI) != ""
	case MaheshvaraContentFile, MaheshvaraContentDocument:
		return ResponsesOutputContent{Type: "file", FileID: part.FileID, FileURL: firstNonEmptyString(part.URI, part.ImageURL), Filename: part.FileName}, part.FileID != "" || part.URI != "" || part.FileData != ""
	case MaheshvaraContentAudio:
		audio := map[string]any{}
		if data := firstNonEmptyString(part.AudioBase64, part.Data); data != "" {
			audio["data"] = data
		}
		if part.AudioURL != "" {
			audio["url"] = part.AudioURL
		}
		if part.MediaType != "" {
			audio["format"] = part.MediaType
		}
		return ResponsesOutputContent{Type: "audio", Audio: audio}, len(audio) > 0
	default:
		return ResponsesOutputContent{}, false
	}
}

// maheshvaraStopToOpenAI 把任意上游的终止原因归一化到 OpenAI finish_reason
// 合法枚举（stop/length/tool_calls/content_filter/function_call）。
// 未知值原样透传会让严格反序列化的客户端 SDK 失败，一律归一到 stop。
func maheshvaraStopToOpenAI(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return reason
	case "tool_use":
		return "tool_calls"
	case "max_tokens", "MAX_TOKENS", "length":
		return "length"
	case "content_filter", "refusal", "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "LANGUAGE":
		return "content_filter"
	default:
		// end_turn/STOP/stop_sequence/"" 映射为 stop；上游新增的未知枚举
		// 原样透传（native finish reason 保真，不静默塌缩成正常结束）。
		if reason == "" || reason == "end_turn" || reason == "STOP" || reason == "stop_sequence" {
			return "stop"
		}
		return reason
	}
}

// maheshvaraStopToClaude 归一化到 Claude stop_reason 合法枚举
// （end_turn/max_tokens/stop_sequence/tool_use/refusal）。
func maheshvaraStopToClaude(reason string) string {
	switch reason {
	case "stop", "STOP", "end_turn", "":
		return "end_turn"
	case "length", "MAX_TOKENS", "max_tokens":
		return "max_tokens"
	case "stop_sequence", "STOP_SEQUENCE":
		return "stop_sequence"
	case "tool_calls", "function_call", "tool_use":
		return "tool_use"
	case "content_filter", "refusal", "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "LANGUAGE":
		return "refusal"
	default:
		// 上游新增的未知枚举原样透传（native 保真）；空串在上方 case 已归一
		// 为 end_turn，不会到达这里。
		return reason
	}
}

// maheshvaraStopToGemini 归一化到 Gemini finishReason 合法枚举
// （STOP/MAX_TOKENS/STOP_SEQUENCE/SAFETY/RECITATION/LANGUAGE/OTHER/BLOCKLIST/
// PROHIBITED_CONTENT/SPII/MALFORMED_FUNCTION_CALL）。
func maheshvaraStopToGemini(reason string) string {
	switch reason {
	case "length", "max_tokens", "MAX_TOKENS":
		return "MAX_TOKENS"
	case "stop_sequence", "STOP_SEQUENCE":
		return "STOP_SEQUENCE"
	case "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL":
		return reason
	case "content_filter", "refusal":
		return "SAFETY"
	default:
		// stop/end_turn/tool_use/tool_calls/""：Gemini 函数调用完成时
		// finishReason 也是 STOP，统一归 STOP；上游新增的未知枚举原样透传
		//（native 保真）。
		if reason == "" || reason == "stop" || reason == "end_turn" || reason == "tool_use" || reason == "tool_calls" {
			return "STOP"
		}
		return reason
	}
}
