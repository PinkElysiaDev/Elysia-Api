// Maheshvara 核心请求 → 目标线制请求：整形入口、透传体与消息/工具/响应格式等请求侧字段的输出。
package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaheshvaraToTargetRequest 把核心请求渲染为目标线制请求体;
// originalResponses 供 Responses 目标对原生输入项保真回放。
func MaheshvaraToTargetRequest(req *MaheshvaraRequest, format FormatType, originalResponses *OpenAIResponsesRequest) ([]byte, error) {
	switch format {
	case FormatClaude:
		return MaheshvaraToAnthropic(req)
	case FormatGemini:
		return MaheshvaraToGemini(req)
	case FormatResponses:
		return MaheshvaraToOpenAIResponses(req, originalResponses)
	default:
		return MaheshvaraToOpenAIChat(req)
	}
}

// MaheshvaraToOpenAIChat 渲染 Chat Completions 请求体。
func MaheshvaraToOpenAIChat(req *MaheshvaraRequest) ([]byte, error) {
	if err := validateMaheshvaraRequestForTarget(req, FormatOpenAIChat); err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":    req.Model,
		"messages": maheshvaraMessagesToOpenAI(req),
	}
	if req.MaxOutputTokens > 0 {
		field := "max_tokens"
		if raw, ok := req.RawExtra["max_tokens_field"]; ok && strings.TrimSpace(string(raw)) == `"max_completion_tokens"` {
			field = "max_completion_tokens"
		}
		out[field] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.Stream {
		out["stream"] = true
		// 流式必须注入 stream_options.include_usage=true：OpenAI 兼容上游默认不在
		// 流式响应里返回 usage，不注入则末尾 chunk 没有 usage，Chat→Responses 转换
		// 时 response.completed.usage 只能全 0。借鉴 cc-switch inject_openai_stream_include_usage。
		out["stream_options"] = StreamOptions{IncludeUsage: true}
	} else if req.StreamOptions != nil {
		out["stream_options"] = StreamOptions{IncludeUsage: req.StreamOptions.IncludeUsage}
	}
	if req.Stop != nil {
		out["stop"] = req.Stop
	}
	if len(req.Tools) > 0 {
		// 遗留 functions 分流：来自旧版 function calling 的工具按旧形态发
		//（PC2.9），其余按现代 tools 发；两种形态可并存。
		var tools, legacyFunctions []map[string]any
		for _, tool := range req.Tools {
			if isLegacyFunctionTool(tool) {
				parameters := tool.Parameters
				if parameters == nil {
					parameters = tool.InputSchema
				}
				legacyFunctions = append(legacyFunctions, map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  parameters,
				})
				continue
			}
			converted, err := maheshvaraToolsToOpenAI([]MaheshvaraTool{tool})
			if err != nil {
				return nil, err
			}
			tools = append(tools, converted...)
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
		if len(legacyFunctions) > 0 {
			out["functions"] = legacyFunctions
		}
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = maheshvaraToolChoiceToOpenAI(req.ToolChoice)
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ResponseFormat != nil {
		out["response_format"] = maheshvaraResponseFormatToOpenAI(req.ResponseFormat)
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		out["reasoning_effort"] = req.Reasoning.Effort
	}
	if req.User != "" {
		out["user"] = req.User
	}
	if req.PromptCacheKey != "" {
		out["prompt_cache_key"] = req.PromptCacheKey
	}
	applyOpenAIRequestExtensionsToBody(out, req)
	return json.Marshal(out)
}

// MaheshvaraToAnthropic 渲染 Anthropic Messages 请求体。
func MaheshvaraToAnthropic(req *MaheshvaraRequest) ([]byte, error) {
	if err := validateMaheshvaraRequestForTarget(req, FormatClaude); err != nil {
		return nil, err
	}
	messages, err := maheshvaraMessagesToClaude(req)
	if err != nil {
		return nil, err
	}
	// max_tokens 仅在未设置（<=0）时兜底到默认值；显式设置的小值必须原样
	// 透传，否则客户端的输出长度限制和按 token 计费都会失真。
	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = ClaudeDefaultMaxTokens
	}
	out := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	// 优先原样回放 Claude 客户端的 system 块数组（保住块级 cache_control
	// 标记——拍平成纯文本会让缓存省钱设置静默失效）；无原始块时退回拼接文本。
	if rawBlocks := req.RawExtra["claude_system_blocks"]; len(rawBlocks) > 0 {
		out["system"] = jsonRawToAny(rawBlocks)
	} else if instructions := maheshvaraResponsesInstructions(req); instructions != "" {
		out["system"] = instructions
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.Stream {
		out["stream"] = true
	}
	if req.Stop != nil {
		out["stop_sequences"] = req.Stop
	}
	if len(req.Tools) > 0 {
		tools, err := maheshvaraToolsToClaude(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if converted := applyClaudeDisableParallelToolUse(maheshvaraToolChoiceToClaude(req.ToolChoice), req.ParallelToolCalls); converted != nil {
		out["tool_choice"] = converted
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		if req.Thinking.Adaptive {
			// 自适应思考：无固定预算，档位走 output_config.effort。
			out["thinking"] = map[string]any{"type": "adaptive"}
			if req.Thinking.Effort != "" {
				out["output_config"] = map[string]any{"effort": req.Thinking.Effort}
			}
		} else {
			budget := req.Thinking.BudgetTokens
			if budget <= 0 {
				budget = budgetFromEffort(req.Thinking.Effort)
			}
			out["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
		out["temperature"] = 1.0
		delete(out, "top_p")
	}
	applyClaudeRequestExtensionsToBody(out, req)
	return json.Marshal(out)
}

// MaheshvaraToGemini 渲染 Gemini generateContent 请求体。
func MaheshvaraToGemini(req *MaheshvaraRequest) ([]byte, error) {
	if err := validateMaheshvaraRequestForTarget(req, FormatGemini); err != nil {
		return nil, err
	}
	contents, err := maheshvaraMessagesToGemini(req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"contents": contents,
	}
	if instructions := maheshvaraResponsesInstructions(req); instructions != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": instructions}},
		}
	}
	cfg := map[string]any{}
	if req.Temperature != nil {
		cfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		cfg["topP"] = *req.TopP
	}
	if req.TopK != nil {
		cfg["topK"] = *req.TopK
	}
	if req.MaxOutputTokens > 0 {
		cfg["maxOutputTokens"] = req.MaxOutputTokens
	}
	if req.ResponseFormat != nil {
		applyMaheshvaraResponseFormatToGemini(cfg, req.ResponseFormat)
	}
	if len(cfg) > 0 {
		out["generationConfig"] = cfg
	}
	if len(req.Tools) > 0 {
		tools, err := maheshvaraToolsToGemini(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		if toolConfig := maheshvaraToolChoiceToGemini(req.ToolChoice); toolConfig != nil {
			out["toolConfig"] = toolConfig
		}
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		thinkingConfig := map[string]any{"includeThoughts": true}
		if req.Thinking.Effort != "" {
			thinkingConfig["thinkingLevel"] = req.Thinking.Effort
		}
		if req.Thinking.BudgetTokens > 0 {
			thinkingConfig["thinkingBudget"] = req.Thinking.BudgetTokens
		}
		if generationConfig, ok := out["generationConfig"].(map[string]any); ok {
			generationConfig["thinkingConfig"] = thinkingConfig
		} else {
			out["generationConfig"] = map[string]any{"thinkingConfig": thinkingConfig}
		}
	}
	applyGeminiRequestExtensionsToBody(out, req)
	return json.Marshal(out)
}

// ResponsesPassthroughBody 透传式构造 Responses 上游请求体：以**原始请求字节**为基底，
// 只覆盖 model 名（模型组路由需要），其余字段（input/tools/reasoning/encrypted_content/
// stream/stream_options/prompt_cache_key 等）原样保留。
//
// 为什么不走 MaheshvaraToOpenAIResponses：那条路把请求拆进 maheshvara 再重建 input，
// 而 codex 的 input 含 reasoning/function_call/encrypted_content 等富项，重建会丢字段或
// 改结构，上游严格校验直接拒 → 1 秒断连。当上游本身就支持 Responses API（用户明确选了
// Responses API 线路）时，零转换透传最稳妥（借鉴 cc-switch 的 should_convert=false 分支）。
//
// modelName 为空时不覆盖 model。
func ResponsesPassthroughBody(originalBody []byte, modelName string) ([]byte, error) {
	out := map[string]any{}
	if err := json.Unmarshal(originalBody, &out); err != nil {
		return nil, fmt.Errorf("failed to parse Responses request for passthrough: %w", err)
	}
	if modelName != "" {
		out["model"] = modelName
	}
	return json.Marshal(out)
}

// PassthroughBody 通用透传：当客户端输入格式与所选上游线路 API 一致时，以原始请求
// 字节为基底直发上游——只改写 model（模型组路由需要），并按需补 stream 标记，其余字段
// （含上游特有的 cache_control / thinking / 各类未知字段）原样保留，避免 unified 中间模型
// 的有损往返。这是把 Responses 的零转换透传推广到 chat_completions / claude / gemini。
//
//   - modelName 为空时不覆盖 model；
//   - ensureStream=true 时确保 stream=true（OpenAI 系同时补 stream_options.include_usage，
//     以便上游回传 usage chunk）。Gemini 由 URL action 决定流式，调用方应传 false；
//   - addStreamOptions 仅对 OpenAI 兼容线路有意义。
func PassthroughBody(originalBody []byte, modelName string, ensureStream, addStreamOptions bool) ([]byte, error) {
	out := map[string]any{}
	if err := json.Unmarshal(originalBody, &out); err != nil {
		return nil, fmt.Errorf("failed to parse request for passthrough: %w", err)
	}
	if modelName != "" {
		out["model"] = modelName
	}
	if ensureStream {
		out["stream"] = true
		if addStreamOptions {
			streamOptions, ok := out["stream_options"].(map[string]any)
			if !ok {
				streamOptions = map[string]any{}
			}
			streamOptions["include_usage"] = true
			out["stream_options"] = streamOptions
		}
	}
	return json.Marshal(out)
}

// NormalizeOpenAIToolCallIDs 对 OpenAI Chat 透传请求做最小修补：仅当检测到
// messages[*].tool_calls[*].id 缺失或为空时，才合成确定性的非空 ID，并同步改写
// 后续 role:"tool" 消息的空 tool_call_id；没有任何缺项时返回与输入完全相同的字节，
// 保证透传路径默认零改动。
func NormalizeOpenAIToolCallIDs(body []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI request for tool call id repair: %w", err)
	}
	messages, ok := root["messages"].([]any)
	if !ok {
		return body, nil
	}

	changed := false
	var active []string
	outputIndex := 0
	for msgIndex, raw := range messages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if callsRaw, hasCalls := m["tool_calls"].([]any); hasCalls && len(callsRaw) > 0 {
			active = active[:0]
			for callIndex, callRaw := range callsRaw {
				call, ok := callRaw.(map[string]any)
				if !ok {
					continue
				}
				id, _ := call["id"].(string)
				if strings.TrimSpace(id) == "" {
					call["id"] = ensureToolCallID("", msgIndex, callIndex)
					changed = true
				}
				if repaired, ok := call["id"].(string); ok && strings.TrimSpace(repaired) != "" {
					active = append(active, repaired)
				}
			}
			outputIndex = 0
			continue
		}
		role, _ := m["role"].(string)
		if role != "tool" && role != "function" {
			continue
		}
		toolCallID, _ := m["tool_call_id"].(string)
		if strings.TrimSpace(toolCallID) != "" || len(active) == 0 {
			continue
		}
		index := outputIndex
		if index >= len(active) {
			index = len(active) - 1
		} else {
			outputIndex++
		}
		m["tool_call_id"] = active[index]
		changed = true
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(root)
}

// MaheshvaraToOpenAIResponses 渲染 Responses 请求体(工具为扁平形状)。
func MaheshvaraToOpenAIResponses(req *MaheshvaraRequest, original *OpenAIResponsesRequest) ([]byte, error) {
	if err := validateMaheshvaraRequestForTarget(req, FormatResponses); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if original != nil {
		b, _ := json.Marshal(original)
		_ = json.Unmarshal(b, &out)
	}

	out["model"] = req.Model
	if instructions := maheshvaraResponsesInstructions(req); instructions != "" {
		out["instructions"] = instructions
	}
	if req.User != "" {
		out["user"] = req.User
	}
	out["input"] = maheshvaraInputToResponses(req)
	if req.MaxOutputTokens > 0 {
		out["max_output_tokens"] = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.Stream {
		out["stream"] = true
	}
	if len(req.Tools) > 0 {
		out["tools"] = maheshvaraToolsToResponses(req.Tools)
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = maheshvaraToolChoiceToResponses(req.ToolChoice)
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ResponseFormat != nil {
		out["text"] = map[string]any{"format": maheshvaraResponseFormatToResponses(req.ResponseFormat)}
	}
	if req.Reasoning != nil {
		reasoning := map[string]any{}
		for k, v := range req.Reasoning.Raw {
			reasoning[k] = v
		}
		if strings.EqualFold(req.Reasoning.Effort, "none") {
			// 上游会把 effort:"none" 静默当成 low 档执行，必须整个省略字段。
			delete(reasoning, "effort")
		} else if req.Reasoning.Effort != "" {
			reasoning["effort"] = req.Reasoning.Effort
		}
		out["reasoning"] = reasoning
	}
	// 携带加密思考历史的请求发给 Responses 上游时，追加 include 让上游
	// 返回加密思考，跨轮续用才可行。
	if maheshvaraRequestHasEncryptedReasoning(req) {
		out["include"] = appendResponsesInclude(out["include"], "reasoning.encrypted_content")
	}
	applyResponsesRequestExtensionsToBody(out, req)
	return json.Marshal(out)
}

func maheshvaraRequestHasEncryptedReasoning(req *MaheshvaraRequest) bool {
	for _, msg := range req.Messages {
		for _, part := range msg.Content {
			if part.Type == MaheshvaraContentReasoning && part.EncryptedContent != "" {
				return true
			}
		}
	}
	for _, item := range req.InputItems {
		if item.Reasoning != nil && item.Reasoning.EncryptedContent != "" {
			return true
		}
	}
	return false
}

func appendResponsesInclude(current any, value string) []string {
	include := make([]string, 0, 4)
	switch typed := current.(type) {
	case []string:
		include = append(include, typed...)
	case []any:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				include = append(include, s)
			}
		}
	case nil:
	default:
		if s, ok := typed.(string); ok {
			include = append(include, s)
		}
	}
	for _, existing := range include {
		if existing == value {
			return include
		}
	}
	return append(include, value)
}

func maheshvaraMessagesToOpenAI(req *MaheshvaraRequest) []map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if req.Instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.Instructions})
	}
	for msgIndex, msg := range req.Messages {
		visibleParts := make([]MaheshvaraContentPart, 0, len(msg.Content))
		var toolOutputs []MaheshvaraContentPart
		var reasoning strings.Builder
		var refusal strings.Builder
		reasoningParts := make([]MaheshvaraContentPart, 0, 2)
		for _, part := range msg.Content {
			if part.Type == MaheshvaraContentReasoning {
				text := firstNonEmptyString(part.ReasoningText, part.Text)
				if text != "" {
					reasoning.WriteString(text)
				}
				reasoningParts = append(reasoningParts, part)
				continue
			}
			if part.Type == MaheshvaraContentRefusal {
				if part.Text != "" {
					refusal.WriteString(part.Text)
				}
				continue
			}
			if part.Type == MaheshvaraContentToolOutput {
				toolOutputs = append(toolOutputs, part)
				continue
			}
			visibleParts = append(visibleParts, part)
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "user"
		}

		// 纯 tool_result 消息（Claude user 轮里的 tool_result block）不生成空的
		// user 消息，只输出下面的 role:"tool" 消息。避免 assistant 的 tool_calls
		// 之后没有对应的 tool 消息而被上游拒（insufficient tool messages）。
		hasRegularContent := len(visibleParts) > 0 || reasoning.Len() > 0 || refusal.Len() > 0 ||
			len(msg.ToolCalls) > 0 || msg.Name != "" || msg.Metadata != nil || msg.CacheControl != nil || msg.ToolCallID != ""
		if (role == "tool" || role == "function") && len(toolOutputs) > 0 {
			// tool 角色消息的内容已全部由下方 toolOutputs 循环输出为 role:"tool" 消息，
			// 不再额外生成一条空 content 的重复 tool 消息。
			hasRegularContent = false
		}

		// OpenAI 要求 role:"tool" 消息紧跟带 tool_calls 的 assistant 消息；
		// Claude user 轮 [tool_result, text] 混合时必须先输出 tool 结果，再输出剩余文本。
		for _, to := range toolOutputs {
			if strings.HasPrefix(to.ToolCallID, legacyFunctionCallIDPrefix) {
				// 遗留 function 结果：role:"function" + name（无 tool_call_id）。
				messages = append(messages, map[string]any{
					"role":    "function",
					"name":    strings.TrimPrefix(to.ToolCallID, legacyFunctionCallIDPrefix),
					"content": to.ToolOutput,
				})
				continue
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": to.ToolCallID,
				"content":      to.ToolOutput,
			})
		}

		if hasRegularContent {
			out := map[string]any{
				"role":    role,
				"content": contentPartsToInterface(visibleParts),
			}
			if len(visibleParts) == 0 && len(msg.ToolCalls) > 0 {
				out["content"] = nil
			}
			if reasoning.Len() > 0 {
				out["reasoning_content"] = reasoning.String()
			}
			// OpenRouter 风格推理明细：逐条回放（含加密思考，provider 门控），
			// 保真优于标量 reasoning_content。
			if details := maheshvaraReasoningToOpenAIDetails(reasoningParts); len(details) > 0 {
				out["reasoning_details"] = details
			}
			if refusal.Len() > 0 {
				out["refusal"] = refusal.String()
			}
			if msg.Name != "" {
				out["name"] = msg.Name
			}
			if msg.Metadata != nil {
				out["metadata"] = msg.Metadata
			}
			if msg.CacheControl != nil {
				out["cache_control"] = msg.CacheControl
			}
			// assistant 历史的消息级 audio 回写:读 message.audio 的客户端
			// (而非 content 数组)需要原位对象。
			if msg.Role == "assistant" {
				if audio := messageAudioField(visibleParts); audio != nil {
					out["audio"] = audio
				}
			}
			if msg.ToolCallID != "" {
				out["tool_call_id"] = msg.ToolCallID
			}
			if len(msg.ToolCalls) > 0 {
				var calls []map[string]any
				for callIndex, call := range msg.ToolCalls {
					arguments := strings.TrimSpace(string(call.Arguments))
					if arguments == "" {
						arguments = call.ArgumentsText
					}
					if arguments == "" {
						arguments = "{}"
					}
					if isLegacyFunctionCall(call) {
						// 遗留 function_call：消息级单对象形态（每条 assistant
						// 消息至多一个，旧客户端语义）。
						out["function_call"] = map[string]any{
							"name":      call.Name,
							"arguments": arguments,
						}
						continue
					}
					callType := firstNonEmptyString(call.Type, MaheshvaraToolFunction)
					wireCall := map[string]any{
						// 即使上游输入遗漏 id，也绝不向 OpenAI 线格式输出空 id；
						// 空串会被严格的上游校验器判为 "missing field id"。
						"id":   ensureToolCallID(call.ID, msgIndex, callIndex),
						"type": callType,
						"function": map[string]any{
							"name":      call.Name,
							"arguments": arguments,
						},
					}
					if signature := maheshvaraSignatureForProvider(call.ThoughtSignature, call.ThoughtSignatureProvider, MaheshvaraSignatureProviderGemini); signature != "" {
						wireCall["extra_content"] = map[string]any{"google": map[string]any{"thought_signature": signature}}
					}
					calls = append(calls, wireCall)
				}
				if len(calls) > 0 {
					out["tool_calls"] = calls
				}
			}
			messages = append(messages, out)
		}
	}
	return messages
}

// imagePartBase64 从图片 part 提取 (mediaType, base64)。优先用结构化的
// ImageBase64+MediaType；否则解析 ImageURL 里内联的 data: URI。
func imagePartBase64(part MaheshvaraContentPart) (string, string) {
	if part.ImageBase64 != "" {
		return part.MediaType, part.ImageBase64
	}
	if uri := firstNonEmptyString(part.ImageURL, part.URI); strings.HasPrefix(uri, "data:") {
		if mt, b64, ok := parseDataURL(uri); ok {
			return mt, b64
		}
	}
	return "", ""
}

// imagePartToOpenAIURL 把图片 part 渲染为 OpenAI image_url 的 url 值
// （http(s) URL 原样；base64 数据组装成 data: URI）。
func imagePartToOpenAIURL(part MaheshvaraContentPart) string {
	if uri := firstNonEmptyString(part.ImageURL, part.URI); uri != "" {
		return uri
	}
	if part.ImageBase64 != "" {
		mt := part.MediaType
		if mt == "" {
			mt = defaultImageMIME
		}
		return "data:" + mt + ";base64," + part.ImageBase64
	}
	return ""
}

// imagePartToClaudeSource 把图片 part 渲染为 Claude image block 的 source。
func imagePartToClaudeSource(part MaheshvaraContentPart) map[string]any {
	if mt, b64 := imagePartBase64(part); b64 != "" {
		if mt == "" {
			mt = defaultImageMIME
		}
		return map[string]any{"type": "base64", "media_type": mt, "data": b64}
	}
	if uri := firstNonEmptyString(part.ImageURL, part.URI); uri != "" {
		return map[string]any{"type": "url", "url": uri}
	}
	return nil
}

// imagePartToGeminiPart 把图片 part 渲染为 Gemini 的 inlineData（base64）或
// fileData（http(s) URL）part。
func imagePartToGeminiPart(part MaheshvaraContentPart) map[string]any {
	if mt, b64 := imagePartBase64(part); b64 != "" {
		if mt == "" {
			mt = defaultImageMIME
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mt, "data": b64}}
	}
	if uri := firstNonEmptyString(part.ImageURL, part.URI); uri != "" {
		fileData := map[string]any{"fileUri": uri}
		if part.MediaType != "" {
			fileData["mimeType"] = part.MediaType
		}
		return map[string]any{"fileData": fileData}
	}
	return nil
}

func maheshvaraMessagesToClaude(req *MaheshvaraRequest) ([]map[string]any, error) {
	var messages []map[string]any
	for _, msg := range req.Messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") || strings.EqualFold(strings.TrimSpace(msg.Role), "developer") {
			continue
		}
		role, _ := normalizeMaheshvaraRole(msg.Role)
		if role == "" {
			role = "user"
		}
		var content []map[string]any
		for _, part := range msg.Content {
			switch part.Type {
			case MaheshvaraContentText:
				if part.Text == "" {
					continue
				}
				block := map[string]any{"type": "text", "text": part.Text}
				if part.CacheControl != nil {
					block["cache_control"] = part.CacheControl
				}
				if len(part.Citations) > 0 {
					block["citations"] = json.RawMessage(part.Citations)
				}
				content = append(content, block)
			case MaheshvaraContentReasoning:
				text := firstNonEmptyString(part.ReasoningText, part.Text)
				signature := claudeThinkingSignatureForPart(part, req.Model)
				if text != "" || signature != "" {
					content = append(content, map[string]any{"type": "thinking", "thinking": text, "signature": signature})
				}
			case MaheshvaraContentImage:
				if src := imagePartToClaudeSource(part); src != nil {
					content = append(content, map[string]any{"type": "image", "source": src})
				}
			case MaheshvaraContentToolOutput:
				if part.ToolCallID != "" {
					content = append(content, map[string]any{"type": "tool_result", "tool_use_id": part.ToolCallID, "content": part.ToolOutput})
				}
			case MaheshvaraContentRefusal:
				if part.Text != "" {
					content = append(content, map[string]any{"type": "text", "text": part.Text})
				}
			case MaheshvaraContentDocument:
				if block := maheshvaraDocumentToClaudeBlock(part); block != nil {
					content = append(content, block)
				}
			case MaheshvaraContentAudio, MaheshvaraContentVideo:
				if block := maheshvaraMediaToClaudeBlock(part); block != nil {
					content = append(content, block)
				}
			default:
				// 服务端工具块（server_tool_use / web_search_tool_result 等）
				// 与未知 Claude 块：整块原样回放（Raw 为完整原始对象）。
				if raw, ok := part.Raw.(map[string]any); ok {
					if _, hasType := raw["type"]; hasType {
						content = append(content, raw)
					}
				}
			}
		}
		for _, call := range msg.ToolCalls {
			var input any = map[string]any{}
			if len(call.Arguments) > 0 {
				_ = json.Unmarshal(call.Arguments, &input)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": input,
			})
		}
		if len(content) == 0 {
			continue
		}
		message := map[string]any{"role": role, "content": content}
		if msg.Name != "" {
			message["name"] = msg.Name
		}
		if msg.CacheControl != nil {
			message["cache_control"] = msg.CacheControl
		}
		if msg.Metadata != nil {
			message["metadata"] = msg.Metadata
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func maheshvaraMessagesToGemini(req *MaheshvaraRequest) ([]map[string]any, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot convert request to Gemini: nil maheshvara request")
	}

	// 构建 tool_call_id → function_name 映射表：Gemini 的 functionResponse.name
	// 必须是函数名（如 "Read"），而非 Anthropic 的 tool_use_id（如 "toolu_01ABC"）。
	toolCallNames := make(map[string]string)
	for _, msg := range req.Messages {
		for _, call := range msg.ToolCalls {
			if call.ID != "" && call.Name != "" {
				toolCallNames[call.ID] = call.Name
			}
		}
	}

	var contents []map[string]any
	for msgIndex, msg := range req.Messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") || strings.EqualFold(strings.TrimSpace(msg.Role), "developer") {
			continue
		}
		role, _ := normalizeMaheshvaraRole(msg.Role)
		if role == "assistant" {
			role = "model"
		} else if role == "" {
			role = "user"
		}
		var parts []map[string]any
		var firstFunctionCallPart map[string]any
		for partIndex, part := range msg.Content {
			switch part.Type {
			case MaheshvaraContentText:
				if part.Text != "" {
					parts = append(parts, map[string]any{"text": part.Text})
				}
			case MaheshvaraContentImage:
				if p := imagePartToGeminiPart(part); p != nil {
					parts = append(parts, p)
				}
			case MaheshvaraContentAudio, MaheshvaraContentVideo, MaheshvaraContentFile, MaheshvaraContentDocument:
				if p := maheshvaraPartToGeminiPart(part); p != nil {
					parts = append(parts, p)
				}
			case MaheshvaraContentReasoning:
				reasoningText := part.ReasoningText
				if reasoningText == "" {
					reasoningText = part.Text
				}
				if reasoningText != "" {
					thought := map[string]any{"text": reasoningText, "thought": true}
					if signature := maheshvaraSignatureForProvider(part.Signature, part.SignatureProvider, MaheshvaraSignatureProviderGemini); signature != "" {
						thought["thoughtSignature"] = signature
					}
					parts = append(parts, thought)
				}
			case MaheshvaraContentRefusal:
				if part.Text != "" {
					parts = append(parts, map[string]any{"text": part.Text})
				}
			case MaheshvaraContentToolOutput:
				responseMap := geminiFunctionResponsePayload(part.ToolOutput)

				// functionResponse.name 必须是函数名，而非 tool_use_id；回查之前的 tool_use 获取函数名。
				name, ok := toolCallNames[part.ToolCallID]
				if !ok {
					name = functionResponseNameFromRaw(part.Raw)
					ok = name != ""
				}
				if !ok || strings.TrimSpace(name) == "" {
					return nil, fmt.Errorf("cannot convert message %d part %d to Gemini: function response tool_use_id %q has no matching function name", msgIndex, partIndex, part.ToolCallID)
				}

				response := map[string]any{"name": name, "response": responseMap}
				responseID := functionResponseIDFromRaw(part.Raw)
				if responseID == "" && part.ToolCallID != "" && part.ToolCallID != name {
					responseID = part.ToolCallID
				}
				if responseID != "" {
					response["id"] = responseID
				}
				parts = append(parts, map[string]any{"functionResponse": response})
			}
		}
		for callIndex, call := range msg.ToolCalls {
			if strings.TrimSpace(call.Name) == "" {
				return nil, fmt.Errorf("cannot convert message %d tool call %d to Gemini: missing function name for call id %q", msgIndex, callIndex, call.ID)
			}
			var args any = map[string]any{}
			if len(call.Arguments) > 0 {
				if err := json.Unmarshal(call.Arguments, &args); err != nil {
					if call.ArgumentsText != "" {
						_ = json.Unmarshal([]byte(call.ArgumentsText), &args)
					}
				}
			}
			functionCall := map[string]any{"name": call.Name, "args": args}
			if call.ID != "" {
				functionCall["id"] = call.ID
			}
			part := map[string]any{"functionCall": functionCall}
			if firstFunctionCallPart == nil {
				firstFunctionCallPart = part
			}
			if signature := maheshvaraSignatureForProvider(call.ThoughtSignature, call.ThoughtSignatureProvider, MaheshvaraSignatureProviderGemini); signature != "" {
				part["thoughtSignature"] = signature
			}
			parts = append(parts, part)
		}
		if firstFunctionCallPart != nil && stringValue(firstFunctionCallPart["thoughtSignature"]) == "" {
			firstFunctionCallPart["thoughtSignature"] = geminiCrossProviderThoughtSignature
		}
		if len(parts) == 0 {
			continue
		}
		if len(contents) > 0 && contents[len(contents)-1]["role"] == role {
			previousParts, _ := contents[len(contents)-1]["parts"].([]map[string]any)
			contents[len(contents)-1]["parts"] = append(previousParts, parts...)
			continue
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("cannot convert request to Gemini: no representable message content")
	}
	return contents, nil
}

func functionResponseNameFromRaw(raw any) string {
	object, _ := raw.(map[string]any)
	if object == nil {
		return ""
	}
	if response, ok := object["functionResponse"].(map[string]any); ok {
		return firstNonEmptyString(stringValue(response["name"]), stringValue(response["function_name"]))
	}
	return firstNonEmptyString(stringValue(object["name"]), stringValue(object["function_name"]))
}

func functionResponseIDFromRaw(raw any) string {
	object, _ := raw.(map[string]any)
	if object == nil {
		return ""
	}
	if response, ok := object["functionResponse"].(map[string]any); ok {
		return firstNonEmptyString(stringValue(response["id"]), stringValue(response["call_id"]))
	}
	return firstNonEmptyString(stringValue(object["id"]), stringValue(object["call_id"]))
}

func geminiFunctionResponsePayload(output string) map[string]any {
	if output == "" {
		return map[string]any{"content": ""}
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(output), &object); err == nil && object != nil {
		return object
	}
	var array []any
	if err := json.Unmarshal([]byte(output), &array); err == nil && array != nil {
		return map[string]any{"result": array}
	}
	return map[string]any{"content": output}
}

func maheshvaraResponsesInstructions(req *MaheshvaraRequest) string {
	if req == nil {
		return ""
	}

	var parts []string
	if strings.TrimSpace(req.Instructions) != "" {
		parts = append(parts, req.Instructions)
	}
	for _, msg := range req.Messages {
		if _, isSystem := normalizeMaheshvaraRole(msg.Role); !isSystem {
			continue
		}
		if text := strings.TrimSpace(maheshvaraText(msg.Content)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func maheshvaraInputToResponses(req *MaheshvaraRequest) any {
	if len(req.InputItems) > 0 {
		var items []map[string]any
		for _, item := range req.InputItems {
			switch item.Type {
			case MaheshvaraInputFunctionCallOutput:
				items = append(items, map[string]any{"type": "function_call_output", "call_id": item.CallID, "output": item.Output})
			default:
				if raw := rawResponsesInputItem(item.RawExtra); raw != nil {
					items = append(items, raw)
					continue
				}
				role := responsesInputRole(item.Role)
				content := maheshvaraContentToResponsesInputContent(role, item.Content)
				if len(content) == 0 {
					continue
				}
				items = append(items, map[string]any{"role": role, "content": content})
			}
		}
		return items
	}

	var items []map[string]any
	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "", "system", "developer":
			continue
		case "tool", "function":
			callID := strings.TrimSpace(msg.ToolCallID)
			// Chat 的 tool 消息经解析后 content 是 tool_output part(载荷在
			// ToolOutput 字段),maheshvaraText 只认 text part 会得到空串。
			output := firstNonEmptyString(maheshvaraText(msg.Content), toolOutputsText(msg.Content))
			if callID == "" {
				items = append(items, map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[tool_output_missing_call_id] %s", output)}}})
				continue
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": callID, "output": output})
			continue
		}

		// Claude tool_result / Gemini functionResponse 解析进 user 角色消息的
		// tool_output part:Responses 的配对靠 call_id(不挑角色),在进入
		// content 转换前先抽出(该转换的 switch 无 ToolOutput 分支,否则被丢)。
		for _, part := range msg.Content {
			if part.Type != MaheshvaraContentToolOutput {
				continue
			}
			callID := strings.TrimSpace(part.ToolCallID)
			if callID == "" {
				callID = strings.TrimSpace(msg.ToolCallID)
			}
			if callID == "" {
				continue
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": callID, "output": firstNonEmptyString(part.ToolOutput, "")})
		}

		content := maheshvaraContentToResponsesInputContent(role, msg.Content)
		if len(content) > 0 {
			items = append(items, map[string]any{"role": responsesInputRole(role), "content": content})
		}
		if role == "assistant" {
			items = append(items, maheshvaraReasoningToResponsesItems(msg)...)
			items = append(items, maheshvaraToolCallsToResponsesItems(msg.ToolCalls)...)
		}
	}
	return items
}

// maheshvaraReasoningToResponsesItems 把 assistant 消息里的推理 parts 汇成
// Responses reasoning item。加密思考按签发方门控回放：仅 openai 签发的密文
// 发还给 openai 系上游（其余厂商密文丢弃、保留摘要文本）；v1 信封无签发方
// 信息时保守回放。
func maheshvaraReasoningToResponsesItems(msg MaheshvaraMessage) []map[string]any {
	var summary []map[string]any
	encrypted := ""
	encryptedProvider := ""
	for _, part := range msg.Content {
		if part.Type != MaheshvaraContentReasoning {
			continue
		}
		if text := firstNonEmptyString(part.ReasoningText, part.Text); text != "" {
			summary = append(summary, map[string]any{"type": "summary_text", "text": text})
		}
		if part.EncryptedContent != "" && encrypted == "" {
			encrypted = part.EncryptedContent
			encryptedProvider = strings.TrimSpace(part.EncryptedProvider)
		}
	}
	if encrypted != "" && encryptedProvider != "" && !strings.EqualFold(encryptedProvider, MaheshvaraSignatureProviderOpenAI) {
		encrypted = ""
	}
	if len(summary) == 0 && encrypted == "" {
		return nil
	}
	item := map[string]any{"type": "reasoning", "summary": summary}
	if encrypted != "" {
		item["encrypted_content"] = encrypted
	}
	return []map[string]any{item}
}

func rawResponsesInputItem(rawExtra map[string]json.RawMessage) map[string]any {
	if len(rawExtra) == 0 || len(rawExtra["raw"]) == 0 {
		return nil
	}
	var item map[string]any
	if err := json.Unmarshal(rawExtra["raw"], &item); err != nil {
		return nil
	}
	return item
}

func responsesInputRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func maheshvaraToolCallsToResponsesItems(calls []MaheshvaraToolCall) []map[string]any {
	items := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		name := strings.TrimSpace(call.Name)
		if callID == "" || name == "" {
			continue
		}
		args := nonEmptyJSONArgs(strings.TrimSpace(string(call.Arguments)))
		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   callID,
			"name":      name,
			"arguments": args,
		})
	}
	return items
}

func maheshvaraContentToResponsesInputContent(role string, parts []MaheshvaraContentPart) []map[string]any {
	role = responsesInputRole(role)
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case MaheshvaraContentText:
			if part.Text != "" {
				out = append(out, map[string]any{"type": responsesTextPartTypeForRole(role), "text": part.Text})
			}
		case MaheshvaraContentReasoning:
			// 推理不作为消息内容降级输出：assistant 的思考由
			// maheshvaraReasoningToResponsesItems 汇成独立 reasoning item
			// （含加密思考回放），避免与正文混杂。
			continue
		case MaheshvaraContentImage:
			if role == "user" {
				if url := imagePartToOpenAIURL(part); url != "" {
					out = append(out, map[string]any{"type": "input_image", "image_url": url})
				}
			}
		case MaheshvaraContentAudio:
			if role == "user" {
				data := firstNonEmptyString(part.AudioBase64, part.Data, part.AudioURL)
				if data != "" {
					out = append(out, map[string]any{"type": "input_audio", "audio": map[string]any{"data": data, "format": firstNonEmptyString(part.MediaType, part.MimeType)}})
				}
			}
		case MaheshvaraContentVideo:
			if role == "user" {
				if url := firstNonEmptyString(part.VideoURL, part.URI, part.ImageURL); url != "" {
					out = append(out, map[string]any{"type": "input_video", "video_url": url})
				}
			}
		case MaheshvaraContentFile:
			if role == "user" {
				item := map[string]any{"type": "input_file"}
				if part.FileID != "" {
					item["file_id"] = part.FileID
				}
				if part.FileName != "" {
					item["filename"] = part.FileName
				}
				if part.FileData != "" {
					item["file_data"] = part.FileData
				}
				if len(item) > 1 {
					out = append(out, item)
				}
			}
		default:
			if refusal := refusalTextFromRaw(part.Raw); role == "assistant" && refusal != "" {
				out = append(out, map[string]any{"type": "refusal", "refusal": refusal})
			}
		}
	}
	return out
}

func responsesTextPartTypeForRole(role string) string {
	if responsesInputRole(role) == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func refusalTextFromRaw(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if strings.ToLower(strings.TrimSpace(stringValue(m["type"]))) != "refusal" {
		return ""
	}
	if refusal := stringValue(m["refusal"]); refusal != "" {
		return refusal
	}
	return stringValue(m["text"])
}

// messageAudioField 从消息 parts 中取首个音频 part 组装 OpenAI 消息级
// audio 对象(id/transcript 经 Raw 回放);无音频返回 nil。
func messageAudioField(parts []MaheshvaraContentPart) map[string]any {
	for _, part := range parts {
		if part.Type != MaheshvaraContentAudio || part.AudioBase64 == "" {
			continue
		}
		audio := map[string]any{"data": part.AudioBase64}
		if part.MediaType != "" {
			audio["format"] = part.MediaType
		}
		if raw := mapValue(part.Raw); raw != nil {
			if id := stringValue(raw["id"]); id != "" {
				audio["id"] = id
			}
			if transcript := stringValue(raw["transcript"]); transcript != "" {
				audio["transcript"] = transcript
			}
		}
		return audio
	}
	return nil
}

// functionToolFields 汇出函数工具在四线渲染间共享的字段集:schema 取
// Parameters 与 InputSchema 的先见者(两键分别是 Chat/Gemini 与 Claude 的
// 源键名),strict 透传三态。
func functionToolFields(tool MaheshvaraTool) (name, description string, schema map[string]any, strict *bool) {
	return tool.Name, tool.Description, firstNonNilMap(tool.Parameters, tool.InputSchema), tool.Strict
}

func maheshvaraToolsToOpenAI(tools []MaheshvaraTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to OpenAI chat completions", tool.Type)
		}
		if isLegacyFunctionTool(tool) {
			// 遗留工具由调用方按 functions 形态分流，此处跳过。
			continue
		}
		name, description, parameters, strict := functionToolFields(tool)
		function := map[string]any{
			"name":        name,
			"description": description,
			"parameters":  parameters,
		}
		if strict != nil {
			function["strict"] = *strict
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return out, nil
}

// legacyFunctionCallIDPrefix 是遗留 function calling 的调用 ID 前缀：
// role:"function" 结果消息没有 tool_call_id，用该前缀 + 函数名合成，
// 与 assistant function_call 的 ID 对齐。
const legacyFunctionCallIDPrefix = "legacy_function:"

func isLegacyFunctionTool(tool MaheshvaraTool) bool {
	return tool.Raw != nil && tool.Raw["legacy_function"] == true
}

func isLegacyFunctionCall(call MaheshvaraToolCall) bool {
	// 只认解析器打的显式标记，不按 ID 前缀猜测——真实工具调用的 id 可能
	// 恰好以 "legacy_function:" 开头（客户端可造），前缀猜测会把它错误地
	// 降级成旧形态。
	return call.Raw != nil && call.Raw["legacy_function"] == true
}

func maheshvaraToolsToClaude(tools []MaheshvaraTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to Claude messages", tool.Type)
		}
		name, description, inputSchema, strict := functionToolFields(tool)
		item := map[string]any{
			"name":         name,
			"description":  description,
			"input_schema": inputSchema,
		}
		if strict != nil {
			item["strict"] = *strict
		}
		if tool.CacheControl != nil {
			item["cache_control"] = tool.CacheControl
		}
		out = append(out, item)
	}
	return out, nil
}

func maheshvaraToolsToGemini(tools []MaheshvaraTool) ([]map[string]any, error) {
	var declarations []map[string]any
	var nativeTools []map[string]any
	for _, tool := range tools {
		if tool.Type != MaheshvaraToolFunction {
			if tool.Raw != nil {
				nativeTools = append(nativeTools, tool.Raw)
				continue
			}
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to Gemini without a native definition", tool.Type)
		}
		name, description, parameters, strict := functionToolFields(tool)
		declaration := map[string]any{
			"name":        name,
			"description": description,
			"parameters":  parameters,
		}
		if strict != nil {
			declaration["strict"] = *strict
		}
		declarations = append(declarations, declaration)
	}
	var out []map[string]any
	if len(declarations) > 0 {
		out = append(out, map[string]any{"functionDeclarations": declarations})
	}
	out = append(out, nativeTools...)
	return out, nil
}

func maheshvaraToolsToResponses(tools []MaheshvaraTool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		// Raw 仅当其已是 Responses 扁平函数形状(Responses 客户端同线解析产物,
		// 可能携带 strict 等扩展字段)时透传;Chat/Claude/Gemini 源形状(嵌套
		// function / input_schema / 无 type)必须经类型化字段重建,否则上游 400。
		if tool.Raw != nil && isResponsesFunctionShape(tool.Raw) {
			out = append(out, tool.Raw)
			continue
		}
		m := map[string]any{"type": tool.Type}
		if tool.Type == MaheshvaraToolFunction {
			m["name"] = tool.Name
			m["description"] = tool.Description
			m["parameters"] = firstNonNilMap(tool.Parameters, tool.InputSchema)
			if tool.Strict != nil {
				m["strict"] = *tool.Strict
			}
		}
		out = append(out, m)
	}
	return out
}

func maheshvaraResponseFormatToOpenAI(f *MaheshvaraResponseFormat) map[string]any {
	if f.Raw != nil && f.Raw["json_schema"] != nil {
		return f.Raw
	}
	if f.Type == "json_schema" {
		return map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":        f.Name,
				"description": f.Description,
				"schema":      f.Schema,
				"strict":      f.Strict,
			},
		}
	}
	return map[string]any{"type": f.Type}
}

func maheshvaraResponseFormatToResponses(f *MaheshvaraResponseFormat) map[string]any {
	if f.Raw != nil {
		return f.Raw
	}
	out := map[string]any{"type": f.Type}
	if f.Name != "" {
		out["name"] = f.Name
	}
	if f.Description != "" {
		out["description"] = f.Description
	}
	if f.Schema != nil {
		out["schema"] = f.Schema
	}
	if f.Strict != nil {
		out["strict"] = *f.Strict
	}
	return out
}

func applyMaheshvaraResponseFormatToGemini(cfg map[string]any, f *MaheshvaraResponseFormat) {
	if f.Type == "json_schema" || f.Type == "json_object" {
		cfg["responseMimeType"] = "application/json"
		if f.Schema != nil {
			cfg["responseSchema"] = f.Schema
		}
	}
}

// maheshvaraReasoningToOpenAIDetails 把推理 parts 重建为 reasoning_details
// 数组（原始条目字段优先，text 用最新值；密文仅回放 openai 签发或来源不明
// 的——其余厂商密文发给 chat 系上游只会被拒）。
func maheshvaraReasoningToOpenAIDetails(parts []MaheshvaraContentPart) []map[string]any {
	details := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		if part.Type != MaheshvaraContentReasoning {
			continue
		}
		text := firstNonEmptyString(part.ReasoningText, part.Text)
		if text != "" {
			detail := map[string]any{"type": "reasoning.text", "text": text}
			if raw, ok := part.Raw.(map[string]any); ok && strings.HasPrefix(stringValue(raw["type"]), "reasoning.") {
				merged := make(map[string]any, len(raw)+1)
				for k, v := range raw {
					merged[k] = v
				}
				merged["text"] = text
				detail = merged
			}
			details = append(details, detail)
		}
		if part.EncryptedContent != "" && (part.EncryptedProvider == "" || strings.EqualFold(part.EncryptedProvider, MaheshvaraSignatureProviderOpenAI)) {
			details = append(details, map[string]any{"type": "reasoning.encrypted", "data": part.EncryptedContent})
		}
	}
	return details
}
