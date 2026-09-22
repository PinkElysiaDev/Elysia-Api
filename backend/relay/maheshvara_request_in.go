// 外部线制请求 → Maheshvara 核心请求：各线解析入口与消息/工具/响应格式等请求侧字段的解析。
package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ConvertRequestToMaheshvara(body []byte, format FormatType, urlModel string) (*MaheshvaraRequest, *OpenAIResponsesRequest, error) {
	var req *MaheshvaraRequest
	var original *OpenAIResponsesRequest
	var err error
	switch format {
	case FormatClaude:
		req, err = AnthropicToMaheshvara(body)
	case FormatGemini:
		req, err = GeminiToMaheshvara(body, urlModel)
	case FormatResponses:
		req, original, err = OpenAIResponsesToMaheshvara(body)
	default:
		req, err = OpenAIChatToMaheshvara(body)
	}
	if err != nil {
		return nil, original, err
	}
	// 直接调用方（含测试）依赖此处补齐；经 ConvertRequestToMaheshvara 进入时幂等。
	completeMaheshvaraToolCallIDs(req)
	return req, original, nil

}

// OpenAIChatToMaheshvara 解析 OpenAI Chat Completions 请求体。
func OpenAIChatToMaheshvara(body []byte) (*MaheshvaraRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI chat request: %w", err)
	}

	req := &MaheshvaraRequest{
		Model:  stringValue(raw["model"]),
		Stream: boolValue(raw["stream"]),
		Stop:   raw["stop"],
		User:   stringValue(raw["user"]),
	}

	// 网关一次只出一份候选：显式拒绝 n != 1，替换「发送 n 却只取
	// choices[0]」的静默丢弃（对齐 DC1a 的诚实语义）。
	if v, ok := numberValue(raw["n"]); ok && int(v) != 1 {
		return nil, fmt.Errorf("chat completions n must be 1: this gateway returns a single candidate per request")
	}

	if v, ok := numberValue(raw["max_completion_tokens"]); ok {
		req.MaxOutputTokens = int(v)
		// 记录原始字段名:出口按原字段回写——o 系列/gpt-5 等新模型严格拒绝
		// max_tokens,降级书写会让这类上游 400。
		if req.RawExtra == nil {
			req.RawExtra = map[string]json.RawMessage{}
		}
		req.RawExtra["max_tokens_field"] = json.RawMessage(`"max_completion_tokens"`)
	} else if v, ok := numberValue(raw["max_tokens"]); ok {
		req.MaxOutputTokens = int(v)
	}
	req.Temperature = floatPointer(raw["temperature"])
	req.TopP = floatPointer(raw["top_p"])
	if so, ok := raw["stream_options"].(map[string]any); ok {
		req.StreamOptions = &MaheshvaraStreamOptions{IncludeUsage: boolValue(so["include_usage"])}
	}
	if raw["tool_choice"] != nil {
		req.ToolChoice = raw["tool_choice"]
	}
	if v, ok := raw["parallel_tool_calls"].(bool); ok {
		req.ParallelToolCalls = &v
	}
	if effort := stringValue(raw["reasoning_effort"]); effort != "" {
		req.Reasoning = &MaheshvaraReasoning{Effort: effort}
		req.Thinking = &MaheshvaraThinking{Enabled: true, Effort: effort}
	}
	if cacheKey := stringValue(raw["prompt_cache_key"]); cacheKey != "" {
		req.PromptCacheKey = cacheKey
	}
	// raw 已解码进 map[string]any，值不会是 json.RawMessage（断言恒失败、retention
	// 静默丢失）。重新 marshal 该值拿回原始 JSON 字节再保留。
	if retentionValue, exists := raw["prompt_cache_retention"]; exists && retentionValue != nil {
		if encoded, err := json.Marshal(retentionValue); err == nil {
			req.PromptCacheRetention = encoded
		}
	}

	req.Messages = parseOpenAIChatMessages(raw["messages"])
	req.Tools = parseOpenAIChatTools(raw["tools"])
	// 遗留 function calling（PC2.9）：顶层 functions 解析为工具（带 legacy
	// 标记），chat 目标按旧形态回发，跨协议目标按现代工具转换。
	if functions, ok := raw["functions"].([]any); ok {
		for _, functionValue := range functions {
			fn, _ := functionValue.(map[string]any)
			if fn == nil || stringValue(fn["name"]) == "" {
				continue
			}
			req.Tools = append(req.Tools, MaheshvaraTool{
				Type:        MaheshvaraToolFunction,
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
				Raw:         map[string]any{"legacy_function": true},
			})
		}
	}
	req.ResponseFormat = parseOpenAIResponseFormat(raw["response_format"])
	applyOpenAIRequestExtensions(raw, req)

	return req, nil
}

// AnthropicToMaheshvara 解析 Anthropic Messages 请求体。
func AnthropicToMaheshvara(body []byte) (*MaheshvaraRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Claude request: %w", err)
	}

	req := &MaheshvaraRequest{
		Model:        stringValue(raw["model"]),
		Instructions: extractTextFromContent(raw["system"]),
		Stream:       boolValue(raw["stream"]),
		Stop:         raw["stop_sequences"],
	}
	if req.Stop == nil {
		req.Stop = raw["stop"]
	}
	if v, ok := numberValue(raw["max_tokens"]); ok {
		req.MaxOutputTokens = int(v)
	}
	req.Temperature = floatPointer(raw["temperature"])
	req.TopP = floatPointer(raw["top_p"])

	req.Messages = parseClaudeMessages(raw["messages"])
	req.Tools = parseClaudeTools(raw["tools"])

	if thinking, ok := raw["thinking"].(map[string]any); ok {
		thinkingType := strings.ToLower(strings.TrimSpace(stringValue(thinking["type"])))
		// adaptive：Claude 4.5+ 自适应思考（无固定预算，effort 走 output_config）。
		req.Thinking = &MaheshvaraThinking{
			Enabled:  thinkingType == "enabled" || thinkingType == "adaptive",
			Adaptive: thinkingType == "adaptive",
		}
		if v, ok := numberValue(thinking["budget_tokens"]); ok {
			req.Thinking.BudgetTokens = int(v)
		}
		if req.Thinking.Enabled {
			req.Reasoning = &MaheshvaraReasoning{Effort: effortFromBudget(req.Thinking.BudgetTokens)}
		}
	}
	// output_config.effort 是 adaptive 思考的档位载体，优先于固定预算折算。
	if outputConfig, ok := raw["output_config"].(map[string]any); ok {
		if effort := stringValue(outputConfig["effort"]); effort != "" {
			if req.Thinking == nil {
				req.Thinking = &MaheshvaraThinking{}
			}
			if !strings.EqualFold(effort, "none") {
				req.Thinking.Effort = effort
			} else if !req.Thinking.Adaptive {
				// 固定预算模式下显式关闭思考。
				req.Thinking.Enabled = false
			}
			req.Reasoning = &MaheshvaraReasoning{Effort: req.Thinking.Effort}
		}
	}
	applyClaudeRequestExtensions(raw, req)

	return req, nil
}

// GeminiToMaheshvara 解析 Gemini generateContent 请求体。
func GeminiToMaheshvara(body []byte, urlModel string) (*MaheshvaraRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini request: %w", err)
	}

	req := &MaheshvaraRequest{
		Model: stringValue(raw["model"]),
	}
	if req.Model == "" {
		req.Model = urlModel
	}
	req.Instructions = extractGeminiSystemInstruction(raw["systemInstruction"])
	req.Messages = parseGeminiContents(raw["contents"])
	req.Tools = parseGeminiTools(raw["tools"])
	req.ToolChoice = raw["toolConfig"]

	var thinkingConfig map[string]any
	if cfg, ok := raw["generationConfig"].(map[string]any); ok {
		if v, ok := cfg["temperature"].(float64); ok {
			req.Temperature = &v
		}
		if v, ok := cfg["topP"].(float64); ok {
			req.TopP = &v
		}
		if v, ok := numberValue(cfg["topK"]); ok {
			topK := int(v)
			req.TopK = &topK
		}
		if v, ok := numberValue(cfg["maxOutputTokens"]); ok {
			req.MaxOutputTokens = int(v)
		}
		req.ResponseFormat = parseGeminiResponseFormat(cfg)
		if configuredThinking, ok := cfg["thinkingConfig"].(map[string]any); ok {
			thinkingConfig = configuredThinking
		}
	}

	// Gemini places thinkingConfig inside generationConfig. Accept the legacy
	// top-level form and thinkingEffort spelling as input compatibility only.
	if legacyThinking, ok := raw["thinkingConfig"].(map[string]any); ok {
		thinkingConfig = legacyThinking
	}
	if thinkingConfig != nil {
		includeThoughts := boolValue(thinkingConfig["includeThoughts"])
		effort := firstNonEmptyString(stringValue(thinkingConfig["thinkingLevel"]), stringValue(thinkingConfig["thinkingEffort"]))
		budget := intValue(thinkingConfig["thinkingBudget"])
		enabled := includeThoughts || effort != "" || budget > 0
		req.Thinking = &MaheshvaraThinking{Enabled: enabled, Effort: effort, BudgetTokens: budget}
		if enabled {
			req.Reasoning = &MaheshvaraReasoning{Effort: effort}
		}
	}
	if err := applyGeminiRequestExtensions(raw, req); err != nil {
		return nil, err
	}

	return req, nil
}

// OpenAIResponsesToMaheshvara 解析 Responses 请求体;同时返回
// 原生请求供同线回放与转换回退。
func OpenAIResponsesToMaheshvara(body []byte) (*MaheshvaraRequest, *OpenAIResponsesRequest, error) {
	var req OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("failed to parse Responses request: %w", err)
	}

	maheshvara := &MaheshvaraRequest{
		Model:              req.Model,
		Instructions:       req.Instructions,
		Temperature:        req.Temperature,
		TopP:               req.TopP,
		ToolChoice:         req.ToolChoice,
		ParallelToolCalls:  req.ParallelToolCalls,
		User:               req.User,
		Metadata:           req.Metadata,
		PreviousResponseID: req.PreviousResponseID,
		Store:              req.Store,
		Include:            req.Include,
		Truncation:         req.Truncation,
		Background:         req.Background,
		Conversation:       req.Conversation,
		Prompt:             req.Prompt,
	}
	if req.Stream != nil {
		maheshvara.Stream = *req.Stream
	}
	if req.MaxOutputTokens != nil {
		maheshvara.MaxOutputTokens = int(*req.MaxOutputTokens)
	}
	if req.Reasoning != nil {
		maheshvara.Reasoning = &MaheshvaraReasoning{Raw: req.Reasoning}
		if effort := stringValue(req.Reasoning["effort"]); effort != "" {
			maheshvara.Reasoning.Effort = effort
			maheshvara.Thinking = &MaheshvaraThinking{Enabled: true, Effort: effort}
		}
	}
	maheshvara.ResponseFormat = parseResponsesTextFormat(req.Text)
	maheshvara.Tools = parseResponsesTools(req.Tools)
	maheshvara.InputItems, maheshvara.Messages = parseResponsesInput(req.Input)
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		applyResponsesRequestExtensions(raw, maheshvara)
	}

	completeMaheshvaraToolCallIDs(maheshvara)
	return maheshvara, &req, nil
}

func parseOpenAIChatMessages(raw any) []MaheshvaraMessage {
	arr, _ := raw.([]any)
	messages := make([]MaheshvaraMessage, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		msg := MaheshvaraMessage{
			Role:         stringValue(m["role"]),
			Content:      interfaceToContentParts(m["content"]),
			ToolCallID:   stringValue(m["tool_call_id"]),
			Name:         stringValue(m["name"]),
			CacheControl: m["cache_control"],
			Metadata:     mapValue(m["metadata"]),
			RawExtra:     rawFields(m),
		}
		if msg.Role == "tool" || msg.Role == "function" {
			// tool/function 消息的 content 是工具结果文本，必须包装成 ToolOutput part，
			// 否则会被当作普通文本，Claude/Gemini 渲染器无法生成 tool_result/functionResponse，
			// 上游会因 tool_use 缺少对应结果而 400。
			output := maheshvaraText(msg.Content)
			// 遗留 function 结果消息用 name 而非 tool_call_id 关联调用：
			// 合成 legacy_function:<name>，与 assistant 侧 function_call 的 ID 对齐。
			callID := msg.ToolCallID
			if callID == "" && msg.Role == "function" && msg.Name != "" {
				callID = legacyFunctionCallIDPrefix + msg.Name
			}
			msg.Content = []MaheshvaraContentPart{{
				Type:       MaheshvaraContentToolOutput,
				ToolCallID: callID,
				ToolOutput: output,
				Raw:        m,
			}}
		}
		// reasoning 前插：Anthropic 要求启用 thinking 时 assistant 消息的 thinking
		// 块必须位于最前，Gemini 的 thought part 同理。
		if reasoning := stringValue(m["reasoning_content"]); strings.TrimSpace(reasoning) != "" {
			msg.Content = append([]MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, Text: reasoning, ReasoningText: reasoning, Raw: m}}, msg.Content...)
		}
		// OpenRouter 风格明细与 opaque 密文优先于标量（每条一 part）。
		if details := openAIReasoningDetailsToParts(m["reasoning_details"]); len(details) > 0 {
			msg.Content = append(details, msg.Content...)
		} else if opaque := stringValue(m["reasoning_opaque"]); opaque != "" {
			msg.Content = append([]MaheshvaraContentPart{{Type: MaheshvaraContentReasoning, EncryptedContent: opaque, EncryptedProvider: MaheshvaraSignatureProviderOpenAI, Raw: m}}, msg.Content...)
		}
		if refusal := firstNonEmptyString(stringValue(m["refusal"]), stringValue(m["refusal_text"])); refusal != "" {
			msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentRefusal, Text: refusal, Raw: m})
		}
		if toolCalls, ok := m["tool_calls"].([]any); ok {
			for _, tc := range toolCalls {
				tcm, _ := tc.(map[string]any)
				if tcm == nil {
					continue
				}
				fn, _ := tcm["function"].(map[string]any)
				arguments := stringValue(fn["arguments"])
				if arguments == "" {
					arguments = "{}"
				}
				thoughtSignature := openAIGoogleThoughtSignature(tcm)
				thoughtSignatureProvider := ""
				if thoughtSignature != "" {
					thoughtSignatureProvider = MaheshvaraSignatureProviderGemini
				}
				msg.ToolCalls = append(msg.ToolCalls, MaheshvaraToolCall{
					ID:                       stringValue(tcm["id"]),
					Type:                     firstNonEmptyString(stringValue(tcm["type"]), MaheshvaraToolFunction),
					Name:                     stringValue(fn["name"]),
					Arguments:                json.RawMessage(arguments),
					ArgumentsText:            arguments,
					ThoughtSignature:         thoughtSignature,
					ThoughtSignatureProvider: thoughtSignatureProvider,
					Raw:                      tcm,
				})
			}
		}
		if msg.Role == "assistant" && m["audio"] != nil {
			msg.Audio = audioConfigFromAny(m["audio"])
			if audioPart := openAIAudioValueToPart(m["audio"]); audioPart != nil {
				msg.Content = append(msg.Content, *audioPart)
			}
		}
		// 遗留 function calling（PC2.9）：assistant 的 function_call 还原为
		// 带 legacy 标记的工具调用，ID 与 role:"function" 结果消息对齐；
		// chat 目标渲染时还原旧形态。
		if fc, ok := m["function_call"].(map[string]any); ok && stringValue(fc["name"]) != "" {
			arguments := stringValue(fc["arguments"])
			if arguments == "" {
				arguments = "{}"
			}
			name := stringValue(fc["name"])
			msg.ToolCalls = append(msg.ToolCalls, MaheshvaraToolCall{
				ID:            legacyFunctionCallIDPrefix + name,
				Type:          MaheshvaraToolFunction,
				Name:          name,
				Arguments:     json.RawMessage(arguments),
				ArgumentsText: arguments,
				Raw:           map[string]any{"legacy_function": true},
			})
		}
		messages = append(messages, msg)
	}
	return messages
}

func openAIGoogleThoughtSignature(toolCall map[string]any) string {
	if toolCall == nil {
		return ""
	}
	extraContent := mapValue(toolCall["extra_content"])
	google := mapValue(extraContent["google"])
	return firstNonEmptyString(
		stringValue(google["thought_signature"]),
		stringValue(google["thoughtSignature"]),
		stringValue(toolCall["thought_signature"]),
		stringValue(toolCall["thoughtSignature"]),
	)
}

func openAIAudioValueToPart(value any) *MaheshvaraContentPart {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil
	}
	data := firstNonEmptyString(stringValue(object["data"]), stringValue(object["audio_data"]), stringValue(object["base64"]))
	url := firstNonEmptyString(stringValue(object["url"]), stringValue(object["audio_url"]))
	transcript := stringValue(object["transcript"])
	if data == "" && url == "" && transcript == "" {
		return nil
	}
	return &MaheshvaraContentPart{
		Type:        MaheshvaraContentAudio,
		AudioURL:    url,
		AudioBase64: data,
		Data:        data,
		Text:        transcript,
		MediaType:   firstNonEmptyString(stringValue(object["format"]), stringValue(object["mime_type"]), stringValue(object["mimeType"])),
		Raw:         object,
	}
}

func parseClaudeMessages(raw any) []MaheshvaraMessage {
	arr, _ := raw.([]any)
	messages := make([]MaheshvaraMessage, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		msg := MaheshvaraMessage{Role: stringValue(m["role"]), RawExtra: rawFields(m)}
		msg.Name = stringValue(m["name"])
		msg.CacheControl = m["cache_control"]
		msg.Metadata = mapValue(m["metadata"])
		if blocks, ok := m["content"].([]any); ok {
			for _, block := range blocks {
				bm, _ := block.(map[string]any)
				if bm == nil {
					continue
				}
				switch stringValue(bm["type"]) {
				case "text":
					part := MaheshvaraContentPart{Type: MaheshvaraContentText, Text: stringValue(bm["text"]), CacheControl: bm["cache_control"], Raw: bm}
					if citations, ok := bm["citations"]; ok && citations != nil {
						if encoded, err := json.Marshal(citations); err == nil {
							part.Citations = encoded
						}
					}
					msg.Content = append(msg.Content, part)
				case "thinking":
					if part, ok := claudeThinkingBlockToPart(bm); ok {
						msg.Content = append(msg.Content, part)
					}
				case "image":
					msg.Content = append(msg.Content, claudeImageBlockToPart(bm))
				case "document", "file":
					msg.Content = append(msg.Content, claudeDocumentBlockToPart(bm))
				case "audio":
					msg.Content = append(msg.Content, claudeMediaBlockToPart(bm, MaheshvaraContentAudio))
				case "video":
					msg.Content = append(msg.Content, claudeMediaBlockToPart(bm, MaheshvaraContentVideo))
				case "tool_use":
					msg.ToolCalls = append(msg.ToolCalls, claudeToolUseBlockToCall(bm))
				case "tool_result":
					msg.Content = append(msg.Content, claudeToolResultBlockToPart(bm))
				case "redacted_thinking":
					if part, ok := claudeRedactedThinkingBlockToPart(bm); ok {
						msg.Content = append(msg.Content, part)
					}
				default:
					// Unknown blocks are kept verbatim as raw parts (never
					// reinterpreted as prompt text): same-wire targets replay
					// them byte-for-byte, cross-wire targets keep their
					// existing unknown-part handling.
					msg.Content = append(msg.Content, MaheshvaraContentPart{Type: stringValue(bm["type"]), Raw: bm})
				}
			}
		} else {
			msg.Content = interfaceToContentParts(m["content"])
		}
		messages = append(messages, msg)
	}
	return messages
}

func parseGeminiContents(raw any) []MaheshvaraMessage {
	arr, _ := raw.([]any)
	messages := make([]MaheshvaraMessage, 0, len(arr))
	for messageIndex, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role := stringValue(m["role"])
		if role == "model" {
			role = "assistant"
		}
		msg := MaheshvaraMessage{Role: role, Name: stringValue(m["name"]), CacheControl: m["cache_control"], RawExtra: rawFields(m)}
		parts, _ := m["parts"].([]any)
		for partIndex, part := range parts {
			pm, _ := part.(map[string]any)
			if pm == nil {
				continue
			}
			if text := stringValue(pm["text"]); text != "" {
				partType := MaheshvaraContentText
				if boolValue(pm["thought"]) {
					partType = MaheshvaraContentReasoning
				}
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: partType, Text: text, ReasoningText: text, Thought: boolValue(pm["thought"]), Signature: stringValue(pm["thoughtSignature"]), SignatureProvider: MaheshvaraSignatureProviderGemini, Raw: pm})
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok {
				argsRaw, _ := json.Marshal(fc["args"])
				if len(argsRaw) == 0 || string(argsRaw) == "null" {
					argsRaw = json.RawMessage([]byte("{}"))
				}
				msg.ToolCalls = append(msg.ToolCalls, MaheshvaraToolCall{
					ID:                       firstNonEmptyString(stringValue(fc["id"]), stringValue(pm["id"]), fmt.Sprintf("call_%d_%d", messageIndex, partIndex)),
					Type:                     MaheshvaraToolFunction,
					Name:                     stringValue(fc["name"]),
					Arguments:                argsRaw,
					ArgumentsText:            string(argsRaw),
					ThoughtSignature:         stringValue(pm["thoughtSignature"]),
					ThoughtSignatureProvider: MaheshvaraSignatureProviderGemini,
					Raw:                      pm,
				})
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok {
				respRaw, _ := json.Marshal(fr["response"])
				msg.Content = append(msg.Content, MaheshvaraContentPart{
					Type:       MaheshvaraContentToolOutput,
					ToolCallID: firstNonEmptyString(stringValue(fr["id"]), stringValue(fr["name"])),
					ToolOutput: string(respRaw),
					Raw:        pm,
				})
			}
			// 多模态：inlineData（base64）/ fileData（URI）→ maheshvara image part。
			if inline, ok := pm["inlineData"].(map[string]any); ok {
				mediaType := firstNonEmptyString(stringValue(inline["mimeType"]), stringValue(inline["mime_type"]))
				partType := MaheshvaraContentImage
				if strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
					partType = MaheshvaraContentAudio
				} else if strings.HasPrefix(strings.ToLower(mediaType), "video/") {
					partType = MaheshvaraContentVideo
				}
				data := stringValue(inline["data"])
				part := MaheshvaraContentPart{Type: partType, MediaType: mediaType, Data: data, Raw: pm}
				switch partType {
				case MaheshvaraContentAudio:
					part.AudioBase64 = data
				case MaheshvaraContentVideo:
					part.VideoBase64 = data
				default:
					part.ImageBase64 = data
				}
				msg.Content = append(msg.Content, part)
			}
			if fileData, ok := pm["fileData"].(map[string]any); ok {
				mediaType := firstNonEmptyString(stringValue(fileData["mimeType"]), stringValue(fileData["mime_type"]))
				partType := MaheshvaraContentFile
				switch {
				case strings.HasPrefix(strings.ToLower(mediaType), "image/"):
					partType = MaheshvaraContentImage
				case strings.HasPrefix(strings.ToLower(mediaType), "audio/"):
					partType = MaheshvaraContentAudio
				case strings.HasPrefix(strings.ToLower(mediaType), "video/"):
					partType = MaheshvaraContentVideo
				}
				uri := firstNonEmptyString(stringValue(fileData["fileUri"]), stringValue(fileData["file_uri"]))
				part := MaheshvaraContentPart{Type: partType, MediaType: mediaType, URI: uri, Raw: pm}
				switch partType {
				case MaheshvaraContentImage:
					part.ImageURL = uri
				case MaheshvaraContentAudio:
					part.AudioURL = uri
				case MaheshvaraContentVideo:
					part.VideoURL = uri
				}
				msg.Content = append(msg.Content, part)
			}
			if code, ok := pm["executableCode"].(map[string]any); ok {
				encoded, _ := json.Marshal(code)
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentFile, Text: string(encoded), Raw: pm})
			}
			if result, ok := pm["codeExecutionResult"].(map[string]any); ok {
				encoded, _ := json.Marshal(result)
				msg.Content = append(msg.Content, MaheshvaraContentPart{Type: MaheshvaraContentToolOutput, ToolOutput: string(encoded), Raw: pm})
			}
		}
		messages = append(messages, msg)
	}
	alignGeminiFunctionResponses(messages)
	return messages
}

// alignGeminiFunctionResponses 把无 id 的 functionResponse 的 ToolCallID
// （此时取的是函数名）回填为同名 functionCall 的实际/合成 ID。
// Gemini 原生语义以 name 关联调用与响应，而 OpenAI/Anthropic 线格式以 id 关联；
// 不对齐时转出的 tool_call_id/tool_use_id 会与调用侧对不上而被上游 400。
func alignGeminiFunctionResponses(messages []MaheshvaraMessage) {
	byName := make(map[string]string)
	for i := range messages {
		for _, call := range messages[i].ToolCalls {
			if call.Name != "" && call.ID != "" {
				if _, exists := byName[call.Name]; !exists {
					byName[call.Name] = call.ID
				}
			}
		}
	}
	if len(byName) == 0 {
		return
	}
	knownIDs := make(map[string]bool, len(byName))
	for _, id := range byName {
		knownIDs[id] = true
	}
	for i := range messages {
		for j := range messages[i].Content {
			part := &messages[i].Content[j]
			if part.Type != MaheshvaraContentToolOutput || part.ToolCallID == "" {
				continue
			}
			// ToolCallID 不是任何已知调用 ID、但与某个函数名一致时，
			// 说明响应侧没有 id、只有 name——替换为该调用的 ID。
			if !knownIDs[part.ToolCallID] {
				if id, ok := byName[part.ToolCallID]; ok {
					part.ToolCallID = id
				}
			}
		}
	}
}

func parseResponsesInput(raw json.RawMessage) ([]MaheshvaraInputItem, []MaheshvaraMessage) {
	if len(raw) == 0 {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		item := MaheshvaraInputItem{
			Type:    MaheshvaraInputMessage,
			Role:    "user",
			Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: text}},
		}
		return []MaheshvaraInputItem{item}, []MaheshvaraMessage{{Role: "user", Content: item.Content}}
	}

	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, nil
	}

	items := make([]MaheshvaraInputItem, 0, len(arr))
	messages := make([]MaheshvaraMessage, 0, len(arr))
	for _, entry := range arr {
		m, _ := entry.(map[string]any)
		if m == nil {
			continue
		}
		itemType := stringValue(m["type"])
		if itemType == "" && m["role"] != nil {
			itemType = MaheshvaraInputMessage
		}
		item := MaheshvaraInputItem{Type: itemType, Role: stringValue(m["role"]), ItemID: stringValue(m["id"])}
		rawEntry, _ := json.Marshal(m)
		item.RawExtra = map[string]json.RawMessage{"raw": rawEntry}
		switch itemType {
		case MaheshvaraInputMessage:
			item.Type = MaheshvaraInputMessage
			item.Content = interfaceToContentParts(m["content"])
			if item.Role == "" {
				item.Role = "user"
			}
			messages = append(messages, MaheshvaraMessage{Role: item.Role, Content: item.Content})
		case MaheshvaraInputFunctionCallOutput:
			item.Type = MaheshvaraInputFunctionCallOutput
			item.CallID = stringValue(m["call_id"])
			item.Output = contentValueToString(m["output"])
			messages = append(messages, MaheshvaraMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    []MaheshvaraContentPart{{Type: MaheshvaraContentToolOutput, ToolCallID: item.CallID, ToolOutput: item.Output, Raw: m}},
			})
		case "function_call":
			item.CallID = firstNonEmptyString(stringValue(m["call_id"]), stringValue(m["id"]))
			item.ItemID = firstNonEmptyString(item.ItemID, item.CallID)
			item.Role = "assistant"
			item.Content = nil
			arguments := contentValueToString(m["arguments"])
			if arguments == "" || arguments == "null" {
				arguments = "{}"
			}
			messages = append(messages, MaheshvaraMessage{
				Role: "assistant",
				ToolCalls: []MaheshvaraToolCall{{
					ID:            item.CallID,
					Type:          MaheshvaraToolFunction,
					Name:          stringValue(m["name"]),
					Arguments:     json.RawMessage(arguments),
					ArgumentsText: arguments,
					Raw:           m,
				}},
			})
		case "reasoning":
			var summaryText strings.Builder
			if summary, ok := m["summary"].([]any); ok {
				for _, summaryItem := range summary {
					summaryMap, _ := summaryItem.(map[string]any)
					summaryText.WriteString(stringValue(summaryMap["text"]))
				}
			}
			encrypted := stringValue(m["encrypted_content"])
			if summaryText.Len() > 0 || encrypted != "" {
				item.Role = "assistant"
				item.Content = []MaheshvaraContentPart{{
					Type:              MaheshvaraContentReasoning,
					Text:              summaryText.String(),
					ReasoningText:     summaryText.String(),
					EncryptedContent:  encrypted,
					EncryptedProvider: MaheshvaraSignatureProviderOpenAI,
					EncryptedModel:    stringValue(m["model"]),
					Thought:           true,
					Raw:               m,
				}}
				messages = append(messages, MaheshvaraMessage{Role: "assistant", Content: item.Content})
			}
		default:
			// Keep provider-specific input items in RawExtra so a Responses
			// target can round-trip them without inventing a lossy translation.
		}
		items = append(items, item)
	}
	return items, messages
}

func parseOpenAIChatTools(raw any) []MaheshvaraTool {
	arr, _ := raw.([]any)
	tools := make([]MaheshvaraTool, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if stringValue(m["type"]) == "function" {
			fn, _ := m["function"].(map[string]any)
			strict := boolPointer(fn["strict"])
			tools = append(tools, MaheshvaraTool{
				Type:        MaheshvaraToolFunction,
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
				InputSchema: mapValue(fn["parameters"]),
				Strict:      strict,
				Provider:    stringValue(m["provider"]),
				Raw:         m,
			})
			continue
		}
		tools = append(tools, MaheshvaraTool{
			Type:     stringValue(m["type"]),
			Provider: stringValue(m["provider"]),
			Config:   m,
			Raw:      m,
		})
	}
	return tools
}

func parseClaudeTools(raw any) []MaheshvaraTool {
	arr, _ := raw.([]any)
	tools := make([]MaheshvaraTool, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		tools = append(tools, MaheshvaraTool{
			Type:         MaheshvaraToolFunction,
			Name:         stringValue(m["name"]),
			Description:  stringValue(m["description"]),
			Parameters:   mapValue(m["input_schema"]),
			InputSchema:  mapValue(m["input_schema"]),
			Strict:       boolPointer(m["strict"]),
			CacheControl: m["cache_control"],
			Raw:          m,
		})
	}
	return tools
}

func parseGeminiTools(raw any) []MaheshvaraTool {
	arr, _ := raw.([]any)
	var tools []MaheshvaraTool
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		fns, _ := m["functionDeclarations"].([]any)
		for _, fnItem := range fns {
			fn, _ := fnItem.(map[string]any)
			if fn == nil {
				continue
			}
			tools = append(tools, MaheshvaraTool{
				Type:        MaheshvaraToolFunction,
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
				InputSchema: mapValue(fn["parameters"]),
				Strict:      boolPointer(fn["strict"]),
				Raw:         fn,
			})
		}
		if len(fns) == 0 {
			toolType := stringValue(m["type"])
			if toolType == "" {
				for key := range m {
					toolType = key
					break
				}
			}
			tools = append(tools, MaheshvaraTool{Type: toolType, Config: m, Raw: m})
		}
	}
	return tools
}

func parseResponsesTools(raw []map[string]any) []MaheshvaraTool {
	tools := make([]MaheshvaraTool, 0, len(raw))
	for _, tool := range raw {
		t := stringValue(tool["type"])
		ct := MaheshvaraTool{Type: t, Raw: tool}
		if t == MaheshvaraToolFunction {
			ct.Name = stringValue(tool["name"])
			ct.Description = stringValue(tool["description"])
			ct.Parameters = mapValue(tool["parameters"])
			ct.InputSchema = mapValue(tool["parameters"])
			ct.Strict = boolPointer(tool["strict"])
		}
		if t == MaheshvaraToolWebSearchPreview {
			ct.SearchContextSize = stringValue(tool["search_context_size"])
		}
		if t == MaheshvaraToolFileSearch {
			if ids, ok := tool["vector_store_ids"].([]any); ok {
				for _, id := range ids {
					ct.VectorStoreIDs = append(ct.VectorStoreIDs, fmt.Sprintf("%v", id))
				}
			}
		}
		if ids, ok := tool["vector_store_ids"].([]string); ok {
			ct.VectorStoreIDs = append(ct.VectorStoreIDs, ids...)
		}
		tools = append(tools, ct)
	}
	return tools
}

// ensureToolCallID 保留非空原始 ID，否则生成确定性的合成 ID。
// 使用 call_<msgIdx>_<callIdx> 与 Gemini 解析器的既有约定保持一致，
// 保证同一次请求内 assistant tool_calls[].id 与后续 role:"tool" 消息对齐。
func ensureToolCallID(id string, msgIndex, callIndex int) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return fmt.Sprintf("call_%d_%d", msgIndex, callIndex)
}

// completeMaheshvaraToolCallIDs 在 maheshvara 解析完成后统一补齐工具调用 ID：
//   - assistant ToolCalls/function_call 的空 ID 按 (消息/调用序号) 合成；
//   - 空 tool_call_id/function_call_output 按顺序关联最近一条 assistant 调用的合成 ID；
//   - 已有非空 ID 一律原样保留。
func completeMaheshvaraToolCallIDs(req *MaheshvaraRequest) {
	if req == nil {
		return
	}
	completeMessageToolCallIDs(req.Messages)
	completeInputItemCallIDs(req.InputItems)
}

func completeMessageToolCallIDs(messages []MaheshvaraMessage) {
	var active []string
	outputIndex := 0
	for msgIndex := range messages {
		msg := &messages[msgIndex]
		if len(msg.ToolCalls) > 0 {
			active = active[:0]
			for callIndex := range msg.ToolCalls {
				msg.ToolCalls[callIndex].ID = ensureToolCallID(msg.ToolCalls[callIndex].ID, msgIndex, callIndex)
				active = append(active, msg.ToolCalls[callIndex].ID)
			}
			outputIndex = 0
			continue
		}
		nextID := func() string {
			if outputIndex < len(active) {
				id := active[outputIndex]
				outputIndex++
				return id
			}
			return ensureToolCallID("", msgIndex, outputIndex)
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if msg.ToolCallID == "" && (role == "tool" || role == "function") {
			if len(active) > 0 {
				msg.ToolCallID = nextID()
			}
		}
		for partIndex := range msg.Content {
			part := &msg.Content[partIndex]
			if part.Type == MaheshvaraContentToolOutput && part.ToolCallID == "" {
				// Responses 输入可能同时带消息级 ToolCallID 与 ToolOutput 内容块，
				// 两者必须共享同一个合成 ID，避免渲染出两条 tool 消息。
				if msg.ToolCallID != "" {
					part.ToolCallID = msg.ToolCallID
				} else if len(active) > 0 {
					part.ToolCallID = nextID()
				}
			}
		}
	}
}

func completeInputItemCallIDs(items []MaheshvaraInputItem) {
	// FIFO 队列按序配对：并行调用 [fc1, fc2, fco1, fco2] 时输出按发出顺序
	// 对应调用（fco1→fc1、fco2→fc2）——只记最近一条调用会把两个输出都配给
	// fc2，工具结果错位。
	var pending []string
	for itemIndex := range items {
		item := &items[itemIndex]
		switch item.Type {
		case "function_call":
			item.CallID = ensureToolCallID(item.CallID, itemIndex, 0)
			pending = append(pending, item.CallID)
		case MaheshvaraInputFunctionCallOutput:
			if item.CallID == "" {
				if len(pending) > 0 {
					item.CallID = pending[0]
					pending = pending[1:]
				} else {
					item.CallID = ensureToolCallID("", itemIndex, 0)
				}
			}
		}
	}
}

// claudeToolUseBlockToCall 把 tool_use 块转为核心工具调用(input 缺失
// 归一为空对象,与严格上游的参数必填约定一致)。
func claudeToolUseBlockToCall(bm map[string]any) MaheshvaraToolCall {
	inputRaw, _ := json.Marshal(bm["input"])
	if len(inputRaw) == 0 || string(inputRaw) == "null" {
		inputRaw = json.RawMessage([]byte("{}"))
	}
	return MaheshvaraToolCall{
		ID:            stringValue(bm["id"]),
		Type:          MaheshvaraToolFunction,
		Name:          stringValue(bm["name"]),
		Arguments:     inputRaw,
		ArgumentsText: string(inputRaw),
		Raw:           bm,
	}
}

// claudeToolResultBlockToPart 把 tool_result 块转为 tool_output part
// (块结构化 content 拍平为字符串形态,Claude API 语义等价)。
func claudeToolResultBlockToPart(bm map[string]any) MaheshvaraContentPart {
	return MaheshvaraContentPart{
		Type:       MaheshvaraContentToolOutput,
		ToolCallID: stringValue(bm["tool_use_id"]),
		ToolOutput: contentValueToString(bm["content"]),
		Raw:        bm,
	}
}

func claudeRedactedThinkingBlockToPart(bm map[string]any) (MaheshvaraContentPart, bool) {
	envelope, ok := decodeMaheshvaraReasoningEnvelope(stringValue(bm["data"]))
	if !ok {
		return MaheshvaraContentPart{}, false
	}
	return MaheshvaraContentPart{
		Type:              MaheshvaraContentReasoning,
		Text:              envelope.Text,
		ReasoningText:     envelope.Text,
		SignatureProvider: MaheshvaraSignatureProviderMaheshvara,
		EncryptedContent:  envelope.EncryptedContent,
		EncryptedProvider: envelope.Provider,
		EncryptedModel:    envelope.Model,
		ReasoningSummary:  envelope.Summary,
		Raw:               bm,
	}, true
}

// claudeThinkingBlockToPart 解析 Claude thinking 块:原生签名回放给
// Anthropic 同线,Maheshvara 信封解密为跨线密文形态;空思考不产生 part。
func claudeThinkingBlockToPart(bm map[string]any) (MaheshvaraContentPart, bool) {
	thinking := stringValue(bm["thinking"])
	signature := stringValue(bm["signature"])
	part := MaheshvaraContentPart{
		Type:              MaheshvaraContentReasoning,
		Text:              thinking,
		ReasoningText:     thinking,
		Signature:         signature,
		SignatureProvider: MaheshvaraSignatureProviderAnthropic,
		Raw:               bm,
	}
	if envelope, ok := decodeMaheshvaraReasoningEnvelope(signature); ok {
		part.applyEnvelope(envelope)
	}
	return part, strings.TrimSpace(part.ReasoningText) != "" || part.EncryptedContent != ""
}

// claudeImageBlockToPart 把 Claude image block（{"source":{...}}）解析为 maheshvara
// image part：base64 source → ImageBase64+MediaType；url source → ImageURL。
func claudeImageBlockToPart(bm map[string]any) MaheshvaraContentPart {
	part := MaheshvaraContentPart{Type: MaheshvaraContentImage, Raw: bm}
	src, _ := bm["source"].(map[string]any)
	if src == nil {
		return part
	}
	switch stringValue(src["type"]) {
	case "base64":
		part.MediaType = firstNonEmptyString(stringValue(src["media_type"]), stringValue(src["mimeType"]))
		part.ImageBase64 = stringValue(src["data"])
	case "url":
		part.ImageURL = stringValue(src["url"])
	default:
		// 未声明 type：尽量从字段推断（data→base64，url→url）。
		if data := stringValue(src["data"]); data != "" {
			part.MediaType = firstNonEmptyString(stringValue(src["media_type"]), stringValue(src["mimeType"]))
			part.ImageBase64 = data
		} else if u := stringValue(src["url"]); u != "" {
			part.ImageURL = u
		}
	}
	return part
}

func parseOpenAIResponseFormat(raw any) *MaheshvaraResponseFormat {
	m, _ := raw.(map[string]any)
	if m == nil {
		return nil
	}
	f := &MaheshvaraResponseFormat{Type: stringValue(m["type"]), Raw: m}
	if js, ok := m["json_schema"].(map[string]any); ok {
		f.Name = stringValue(js["name"])
		f.Description = stringValue(js["description"])
		f.Schema = mapValue(js["schema"])
		if strict, ok := js["strict"].(bool); ok {
			f.Strict = &strict
		}
	}
	return f
}

func parseResponsesTextFormat(raw map[string]any) *MaheshvaraResponseFormat {
	if raw == nil {
		return nil
	}
	format, _ := raw["format"].(map[string]any)
	if format == nil {
		return nil
	}
	return &MaheshvaraResponseFormat{
		Type:        stringValue(format["type"]),
		Name:        stringValue(format["name"]),
		Description: stringValue(format["description"]),
		Schema:      mapValue(format["schema"]),
		Raw:         format,
	}
}

func parseGeminiResponseFormat(cfg map[string]any) *MaheshvaraResponseFormat {
	mime := stringValue(cfg["responseMimeType"])
	schema := mapValue(cfg["responseSchema"])
	if mime == "" && schema == nil {
		return nil
	}
	formatType := "text"
	if strings.Contains(mime, "json") {
		formatType = "json_schema"
	}
	return &MaheshvaraResponseFormat{Type: formatType, Schema: schema, Raw: cfg}
}

func extractGeminiSystemInstruction(raw any) string {
	m, _ := raw.(map[string]any)
	if m == nil {
		return ""
	}
	// Gemini 的 Part 没有 type 判别字段(与 Claude content block 不同),
	// extractTextFromContent 按 type=="text" 过滤会全部落空,需直接取 text 键。
	parts, _ := m["parts"].([]any)
	var builder strings.Builder
	for _, part := range parts {
		if partMap, ok := part.(map[string]any); ok {
			if text, ok := partMap["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}

// openAIReasoningDetailsToParts 把 OpenRouter 风格的 reasoning_details 逐条
// 解析为推理 parts（每条一 part，不合并不去重）；text/summary 走明文，
// encrypted/data 走密文（签发方记为 openai）。
func openAIReasoningDetailsToParts(raw any) []MaheshvaraContentPart {
	var arr []map[string]any
	switch typed := raw.(type) {
	case []map[string]any:
		arr = typed
	case []any:
		arr = make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				arr = append(arr, m)
			}
		}
	}
	parts := make([]MaheshvaraContentPart, 0, len(arr))
	for _, detail := range arr {
		if detail == nil {
			continue
		}
		text := firstNonEmptyString(stringValue(detail["text"]), stringValue(detail["summary"]))
		encrypted := firstNonEmptyString(stringValue(detail["encrypted_content"]), stringValue(detail["data"]))
		if text == "" && encrypted == "" {
			continue
		}
		part := MaheshvaraContentPart{Type: MaheshvaraContentReasoning, Raw: detail, Thought: true}
		if text != "" {
			part.Text = text
			part.ReasoningText = text
		}
		if encrypted != "" {
			part.EncryptedContent = encrypted
			part.EncryptedProvider = MaheshvaraSignatureProviderOpenAI
		}
		parts = append(parts, part)
	}
	return parts
}

// isResponsesFunctionShape 判断工具 Raw 是否已是 Responses 的扁平函数形状
// ({type:"function", name, ...} 且无 Chat 的嵌套 function 键)。
func isResponsesFunctionShape(raw map[string]any) bool {
	if stringValue(raw["type"]) != MaheshvaraToolFunction {
		return false
	}
	if _, nested := raw["function"]; nested {
		return false
	}
	return strings.TrimSpace(stringValue(raw["name"])) != ""
}
