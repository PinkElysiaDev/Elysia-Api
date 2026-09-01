package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
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

func MaheshvaraToOpenAIChat(req *MaheshvaraRequest) ([]byte, error) {
	if err := validateMaheshvaraRequestForTarget(req, FormatOpenAIChat); err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":    req.Model,
		"messages": maheshvaraMessagesToOpenAI(req),
	}
	if req.MaxOutputTokens > 0 {
		out["max_tokens"] = req.MaxOutputTokens
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
		out["tool_choice"] = maheshvaraToolChoiceToOpenAI(req.ToolChoice)
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
					if strings.TrimSpace(part.ReasoningText) != "" || part.EncryptedContent != "" {
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
					inputRaw, _ := json.Marshal(bm["input"])
					if len(inputRaw) == 0 || string(inputRaw) == "null" {
						inputRaw = json.RawMessage([]byte("{}"))
					}
					msg.ToolCalls = append(msg.ToolCalls, MaheshvaraToolCall{
						ID:            stringValue(bm["id"]),
						Type:          MaheshvaraToolFunction,
						Name:          stringValue(bm["name"]),
						Arguments:     inputRaw,
						ArgumentsText: string(inputRaw),
						Raw:           bm,
					})
				case "tool_result":
					msg.Content = append(msg.Content, MaheshvaraContentPart{
						Type:       MaheshvaraContentToolOutput,
						ToolCallID: stringValue(bm["tool_use_id"]),
						ToolOutput: contentValueToString(bm["content"]),
						Raw:        bm,
					})
				case "redacted_thinking":
					// Only Maheshvara-owned envelopes are decoded. Arbitrary provider
					// ciphertext remains filtered and never becomes prompt text.
					if envelope, ok := decodeMaheshvaraReasoningEnvelope(stringValue(bm["data"])); ok {
						msg.Content = append(msg.Content, MaheshvaraContentPart{
							Type:              MaheshvaraContentReasoning,
							Text:              envelope.Text,
							ReasoningText:     envelope.Text,
							SignatureProvider: MaheshvaraSignatureProviderMaheshvara,
							EncryptedContent:  envelope.EncryptedContent,
							EncryptedProvider: envelope.Provider,
							EncryptedModel:    envelope.Model,
							ReasoningSummary:  envelope.Summary,
							Raw:               bm,
						})
					}
					continue
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

// firstNonEmptyString 返回第一个非空字符串。
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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

// parseDataURL 解析 data:[<mediatype>][;base64],<data> URI，返回媒体类型与原始数据。
func parseDataURL(u string) (mediaType, data string, ok bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", "", false
	}
	rest := u[len("data:"):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", "", false
	}
	meta := strings.TrimSuffix(rest[:comma], ";base64")
	return meta, rest[comma+1:], true
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
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "tool" || role == "function" || role == "" {
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
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "assistant" {
			role = "model"
		} else if role == "tool" || role == "function" || role == "developer" || role == "" {
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
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role != "system" && role != "developer" {
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
			output := maheshvaraText(msg.Content)
			if callID == "" {
				items = append(items, map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[tool_output_missing_call_id] %s", output)}}})
				continue
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": callID, "output": output})
			continue
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
		args := strings.TrimSpace(string(call.Arguments))
		if args == "" {
			args = "{}"
		}
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
		parameters := tool.Parameters
		if parameters == nil {
			parameters = tool.InputSchema
		}
		function := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  parameters,
		}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
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
		inputSchema := tool.InputSchema
		if inputSchema == nil {
			inputSchema = tool.Parameters
		}
		item := map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": inputSchema,
		}
		if tool.Strict != nil {
			item["strict"] = *tool.Strict
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
		parameters := tool.Parameters
		if parameters == nil {
			parameters = tool.InputSchema
		}
		declaration := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  parameters,
		}
		if tool.Strict != nil {
			declaration["strict"] = *tool.Strict
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
		if tool.Raw != nil {
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

func extractGeminiSystemInstruction(raw any) string {
	m, _ := raw.(map[string]any)
	if m == nil {
		return ""
	}
	return extractTextFromContent(m["parts"])
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// effortFromBudget 把固定思考预算量化为 effort 档位（对齐 Claude 官方
// budget 档：1024/4096/16384，超过 high 归 xhigh）。
func effortFromBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	if budget <= effortBudgetLow {
		return "low"
	}
	if budget <= effortBudgetMedium {
		return "medium"
	}
	if budget <= effortBudgetHigh {
		return "high"
	}
	return "xhigh"
}

// budgetFromEffort 把 effort 档位量化回固定预算（low/medium/high/xhigh →
// Claude 官方档位预算；xhigh 与 max 共享 32000 上限）。
func budgetFromEffort(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "minimal", "min":
		return effortBudgetLow
	case "medium":
		return effortBudgetMedium
	case "high":
		return effortBudgetHigh
	case "xhigh", "max":
		return effortBudgetMax
	default:
		return EffortBudgetDefault
	}
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
		// 服务端工具项（web_search_call 等）没有类型化载荷字段：整项原始
		// 对象挂到 Raw，Responses 目标渲染时原样回放，不再只剩空壳。
		if index < len(resp.RawOutputs) {
			switch item.Type {
			case "message", "reasoning", "function_call", "custom_tool_call":
			default:
				citem.Raw = resp.RawOutputs[index]
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
				citem.Content = append(citem.Content, MaheshvaraContentPart{Type: MaheshvaraContentReasoning, Text: summaryText.String(), ReasoningText: summaryText.String(), EncryptedContent: item.EncryptedContent, EncryptedProvider: MaheshvaraSignatureProviderOpenAI, EncryptedModel: resp.Model, ReasoningSummary: citem.Summary})
			}
		}
		out.Output = append(out.Output, citem)
		if out.Usage != nil {
			switch item.Type {
			case "web_search_call":
				out.Usage.WebSearchCallCount++
			case "file_search_call":
				out.Usage.FileSearchCallCount++
			case "image_generation_call":
				out.Usage.ImageGenerationCallCount++
			}
		}
	}
	return out, nil
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
			arguments := strings.TrimSpace(string(item.Arguments))
			if arguments == "" {
				arguments = "{}"
			}
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
	appendMapContent := func(raw map[string]any) error {
		if raw == nil {
			return nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		var block ClaudeContent
		if err := json.Unmarshal(encoded, &block); err != nil {
			return err
		}
		content = append(content, block)
		return nil
	}
	for _, item := range resp.Output {
		switch item.Type {
		case MaheshvaraOutputMessage:
			for _, part := range item.Content {
				switch part.Type {
				case MaheshvaraContentText:
					if part.Text != "" {
						content = append(content, ClaudeContent{Type: "text", Text: part.Text, Citations: part.Citations})
					}
				case MaheshvaraContentReasoning:
					text := firstNonEmptyString(part.ReasoningText, part.Text)
					if signature := claudeThinkingSignatureForPart(part, resp.Model); text != "" || signature != "" {
						content = append(content, ClaudeContent{Type: "thinking", Thinking: text, Signature: signature})
					}
				case MaheshvaraContentRefusal:
					if part.Text != "" {
						content = append(content, ClaudeContent{Type: "text", Text: part.Text})
					}
				case MaheshvaraContentImage:
					if source := imagePartToClaudeSource(part); source != nil {
						if err := appendMapContent(map[string]any{"type": "image", "source": source}); err != nil {
							return nil, err
						}
					}
				case MaheshvaraContentDocument, MaheshvaraContentFile:
					if block := maheshvaraDocumentToClaudeBlock(part); block != nil {
						if err := appendMapContent(block); err != nil {
							return nil, err
						}
					}
				case MaheshvaraContentAudio, MaheshvaraContentVideo:
					if block := maheshvaraMediaToClaudeBlock(part); block != nil {
						if err := appendMapContent(block); err != nil {
							return nil, err
						}
					}
				case MaheshvaraContentToolOutput:
					if part.ToolCallID != "" {
						content = append(content, ClaudeContent{Type: "tool_result", ToolUseID: part.ToolCallID, Content: part.ToolOutput})
					}
				default:
					// 服务端工具块与未知 Claude 块：整块原样回放。
					if raw, ok := part.Raw.(map[string]any); ok {
						if _, hasType := raw["type"]; hasType {
							if err := appendMapContent(raw); err != nil {
								return nil, err
							}
						}
					}
				}
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

func maheshvaraUsageFromOpenAIUsage(usage Usage) *MaheshvaraUsage {
	var promptDetails PromptTokensDetails
	if usage.PromptTokensDetails != nil {
		promptDetails = *usage.PromptTokensDetails
	}
	if usage.InputTokensDetails != nil && (usage.InputTokensDetails.CachedTokens > 0 || usage.InputTokensDetails.TextTokens > 0) {
		promptDetails = *usage.InputTokensDetails
	}
	var completionDetails CompletionTokensDetails
	if usage.CompletionTokensDetails != nil {
		completionDetails = *usage.CompletionTokensDetails
	}
	u := &MaheshvaraUsage{
		InputTokens:              usage.PromptTokens,
		OutputTokens:             usage.CompletionTokens,
		TotalTokens:              usage.TotalTokens,
		CachedInputTokens:        max(usage.CachedTokens, usage.PromptCacheHitTokens),
		ReasoningTokens:          completionDetails.ReasoningTokens,
		AcceptedPredictionTokens: completionDetails.AcceptedPredictionTokens,
		RejectedPredictionTokens: completionDetails.RejectedPredictionTokens,
		TextInputTokens:          promptDetails.TextTokens,
		AudioInputTokens:         promptDetails.AudioTokens,
		ImageInputTokens:         promptDetails.ImageTokens,
		TextOutputTokens:         completionDetails.TextTokens,
		AudioOutputTokens:        completionDetails.AudioTokens,
		ImageOutputTokens:        completionDetails.ImageTokens,
		Raw:                      usage.RawFields,
		Source:                   usageSourceProviderResponse,
	}
	if u.CachedInputTokens == 0 {
		u.CachedInputTokens = max(promptDetails.CachedTokens, promptDetails.CacheReadTokens)
	}
	if u.CacheCreationInputTokens == 0 {
		u.CacheCreationInputTokens = promptDetails.CachedCreationTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

func maheshvaraUsageFromClaudeUsage(usage ClaudeUsage) *MaheshvaraUsage {
	input := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if usage.CacheCreationInputTokens == 0 && usage.CacheCreation != nil {
		// Anthropic 官方响应里 cache_creation.ephemeral_* 是 cache_creation_input_tokens
		// 的明细拆分，两者同时返回且相等；只在总数缺失时才用明细求和，避免双重计入。
		input += usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
	}
	u := &MaheshvaraUsage{
		InputTokens:              input,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              input + usage.OutputTokens,
		CachedInputTokens:        usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		Source:                   usageSourceProviderResponse,
	}
	if usage.CacheCreation != nil {
		// 双 TTL 桶明细保真（ephemeral_5m / ephemeral_1h）。
		u.CacheCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
		u.CacheCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
	}
	if usage.ServerToolUse != nil {
		u.WebSearchCallCount = usage.ServerToolUse.WebSearchRequests
	}
	return u
}

func maheshvaraUsageFromGeminiUsage(usage GeminiUsageMeta) *MaheshvaraUsage {
	u := &MaheshvaraUsage{
		InputTokens:       usage.PromptTokenCount + usage.ToolUsePromptTokenCount,
		OutputTokens:      usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
		TotalTokens:       usage.TotalTokenCount,
		CachedInputTokens: usage.CachedContentTokenCount,
		ReasoningTokens:   usage.ThoughtsTokenCount,
		ToolUseTokens:     usage.ToolUsePromptTokenCount,
		Source:            usageSourceProviderResponse,
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	for _, detail := range usage.PromptTokensDetails {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			u.TextInputTokens += detail.TokenCount
		case "IMAGE":
			u.ImageInputTokens += detail.TokenCount
		case "AUDIO":
			u.AudioInputTokens += detail.TokenCount
		}
	}
	for _, detail := range usage.CandidatesTokensDetails {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			u.TextOutputTokens += detail.TokenCount
		case "IMAGE":
			u.ImageOutputTokens += detail.TokenCount
		case "AUDIO":
			u.AudioOutputTokens += detail.TokenCount
		}
	}
	return u
}

func maheshvaraUsageFromResponsesUsage(usage *ResponsesUsage) *MaheshvaraUsage {
	if usage == nil {
		return nil
	}
	u := &MaheshvaraUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		Source:       usageSourceProviderResponse,
	}
	if usage.InputTokensDetails != nil {
		u.CachedInputTokens = usage.InputTokensDetails.CachedTokens
	}
	if usage.OutputTokensDetails != nil {
		u.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

func openAIUsageFromMaheshvara(u *MaheshvaraUsage) Usage {
	if u == nil {
		return Usage{}
	}
	// details 仅在有值时输出（指针 omitempty）——空对象会覆盖 RawFields
	// 透传的同键子对象。
	var promptDetails *PromptTokensDetails
	if u.CachedInputTokens > 0 || u.TextInputTokens > 0 || u.AudioInputTokens > 0 || u.ImageInputTokens > 0 {
		promptDetails = &PromptTokensDetails{
			CachedTokens: u.CachedInputTokens,
			TextTokens:   u.TextInputTokens,
			AudioTokens:  u.AudioInputTokens,
			ImageTokens:  u.ImageInputTokens,
		}
	}
	var completionDetails *CompletionTokensDetails
	if u.ReasoningTokens > 0 || u.TextOutputTokens > 0 || u.AudioOutputTokens > 0 || u.ImageOutputTokens > 0 || u.AcceptedPredictionTokens > 0 || u.RejectedPredictionTokens > 0 {
		completionDetails = &CompletionTokensDetails{
			ReasoningTokens:          u.ReasoningTokens,
			TextTokens:               u.TextOutputTokens,
			AudioTokens:              u.AudioOutputTokens,
			ImageTokens:              u.ImageOutputTokens,
			AcceptedPredictionTokens: u.AcceptedPredictionTokens,
			RejectedPredictionTokens: u.RejectedPredictionTokens,
		}
	}
	return Usage{
		PromptTokens:            u.InputTokens,
		CompletionTokens:        u.OutputTokens,
		TotalTokens:             valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		CachedTokens:            u.CachedInputTokens,
		PromptTokensDetails:     promptDetails,
		CompletionTokensDetails: completionDetails,
		// 上游新增的未知计数键原样透传（原始对象为底，类型化字段覆盖）。
		RawFields: u.Raw,
	}
}

func claudeUsageFromMaheshvara(u *MaheshvaraUsage) ClaudeUsage {
	if u == nil {
		return ClaudeUsage{}
	}
	// Claude 协议中 input_tokens 不含缓存 token（缓存读/写是独立字段），而 maheshvara
	// 的 InputTokens 是含缓存的总数；还原时剔除缓存部分，避免 Claude→maheshvara→Claude
	// 往返后 input_tokens 与 cache 字段重复计入。
	input := u.InputTokens - u.CachedInputTokens - u.CacheCreationInputTokens
	if input < 0 {
		input = u.InputTokens
	}
	usage := ClaudeUsage{
		InputTokens:              input,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CachedInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
	if u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 {
		// 双 TTL 桶明细回写（仅在有明细时输出，避免空对象）。
		usage.CacheCreation = &ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: u.CacheCreation5mTokens,
			Ephemeral1hInputTokens: u.CacheCreation1hTokens,
		}
	}
	if u.WebSearchCallCount > 0 {
		usage.ServerToolUse = &ClaudeServerToolUse{WebSearchRequests: u.WebSearchCallCount}
	}
	return usage
}

func geminiUsageFromMaheshvara(u *MaheshvaraUsage) GeminiUsageMeta {
	if u == nil {
		return GeminiUsageMeta{}
	}
	// maheshvara 的 Input/Output 是含 tool/thought 分量的总数；Gemini 各计数器
	// 是独立分项，还原时剔除已单列的分量，避免往返双计（对齐 Claude 缓存减法）。
	promptTokens := u.InputTokens - u.ToolUseTokens
	if promptTokens < 0 {
		promptTokens = u.InputTokens
	}
	candidateTokens := u.OutputTokens - u.ReasoningTokens
	if candidateTokens < 0 {
		candidateTokens = u.OutputTokens
	}
	meta := GeminiUsageMeta{
		PromptTokenCount:        promptTokens,
		ToolUsePromptTokenCount: u.ToolUseTokens,
		CandidatesTokenCount:    candidateTokens,
		TotalTokenCount:         valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		ThoughtsTokenCount:      u.ReasoningTokens,
		CachedContentTokenCount: u.CachedInputTokens,
	}
	// 模态明细回写（有值才输出，保持 usageMetadata 紧凑）。
	appendModality := func(target *[]GeminiTokenDetail, modality string, count int) {
		if count > 0 {
			*target = append(*target, GeminiTokenDetail{Modality: modality, TokenCount: count})
		}
	}
	appendModality(&meta.PromptTokensDetails, "TEXT", u.TextInputTokens)
	appendModality(&meta.PromptTokensDetails, "IMAGE", u.ImageInputTokens)
	appendModality(&meta.PromptTokensDetails, "AUDIO", u.AudioInputTokens)
	appendModality(&meta.CandidatesTokensDetails, "TEXT", u.TextOutputTokens)
	appendModality(&meta.CandidatesTokensDetails, "IMAGE", u.ImageOutputTokens)
	appendModality(&meta.CandidatesTokensDetails, "AUDIO", u.AudioOutputTokens)
	return meta
}

func responsesUsageFromMaheshvara(u *MaheshvaraUsage) *ResponsesUsage {
	if u == nil {
		return nil
	}
	out := &ResponsesUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
	}
	if u.CachedInputTokens > 0 {
		out.InputTokensDetails = &ResponsesInputTokensDetails{CachedTokens: u.CachedInputTokens}
	}
	if u.ReasoningTokens > 0 {
		out.OutputTokensDetails = &ResponsesOutputTokensDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return out
}

func valueOrSum(total, input, output int) int {
	if total > 0 {
		return total
	}
	return input + output
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
		// 上游新增的未知枚举原样透传（native 保真）；空值保持 end_turn。
		if reason == "" {
			return "end_turn"
		}
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

func jsonRawToAny(raw json.RawMessage) any {
	var out any
	if len(raw) > 0 && json.Unmarshal(raw, &out) == nil {
		return out
	}
	return map[string]any{}
}
