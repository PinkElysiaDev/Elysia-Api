package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func ConvertRequestToCanonical(body []byte, format FormatType, urlModel string) (*CanonicalRequest, *OpenAIResponsesRequest, error) {
	switch format {
	case FormatClaude:
		req, err := ClaudeRequestToCanonical(body)
		return req, nil, err
	case FormatGemini:
		req, err := GeminiRequestToCanonical(body, urlModel)
		return req, nil, err
	case FormatResponses:
		return ResponsesRequestToCanonical(body)
	default:
		req, err := OpenAIChatRequestToCanonical(body)
		return req, nil, err
	}
}

func CanonicalToTargetRequest(req *CanonicalRequest, format FormatType, originalResponses *OpenAIResponsesRequest) ([]byte, error) {
	switch format {
	case FormatClaude:
		return CanonicalToClaudeRequest(req)
	case FormatGemini:
		return CanonicalToGeminiRequest(req)
	case FormatResponses:
		return CanonicalToResponsesRequest(req, originalResponses)
	default:
		return CanonicalToOpenAIChatRequest(req)
	}
}

func OpenAIChatRequestToCanonical(body []byte) (*CanonicalRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI chat request: %w", err)
	}

	req := &CanonicalRequest{
		Model:  stringValue(raw["model"]),
		Stream: boolValue(raw["stream"]),
		Stop:   raw["stop"],
		User:   stringValue(raw["user"]),
	}

	if v, ok := numberValue(raw["max_completion_tokens"]); ok {
		req.MaxOutputTokens = int(v)
	} else if v, ok := numberValue(raw["max_tokens"]); ok {
		req.MaxOutputTokens = int(v)
	}
	if v, ok := raw["temperature"].(float64); ok {
		req.Temperature = &v
	}
	if v, ok := raw["top_p"].(float64); ok {
		req.TopP = &v
	}
	if so, ok := raw["stream_options"].(map[string]any); ok {
		req.StreamOptions = &CanonicalStreamOptions{IncludeUsage: boolValue(so["include_usage"])}
	}
	if raw["tool_choice"] != nil {
		req.ToolChoice = raw["tool_choice"]
	}
	if v, ok := raw["parallel_tool_calls"].(bool); ok {
		req.ParallelToolCalls = &v
	}
	if effort := stringValue(raw["reasoning_effort"]); effort != "" {
		req.Reasoning = &CanonicalReasoning{Effort: effort}
		req.Thinking = &CanonicalThinking{Enabled: true, Effort: effort}
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
	req.ResponseFormat = parseOpenAIResponseFormat(raw["response_format"])

	return req, nil
}

func ClaudeRequestToCanonical(body []byte) (*CanonicalRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Claude request: %w", err)
	}

	req := &CanonicalRequest{
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
	if v, ok := raw["temperature"].(float64); ok {
		req.Temperature = &v
	}
	if v, ok := raw["top_p"].(float64); ok {
		req.TopP = &v
	}

	req.Messages = parseClaudeMessages(raw["messages"])
	req.Tools = parseClaudeTools(raw["tools"])

	if thinking, ok := raw["thinking"].(map[string]any); ok {
		enabled := strings.EqualFold(stringValue(thinking["type"]), "enabled")
		budget := 0
		if v, ok := numberValue(thinking["budget_tokens"]); ok {
			budget = int(v)
		}
		req.Thinking = &CanonicalThinking{Enabled: enabled, BudgetTokens: budget}
		if enabled {
			req.Reasoning = &CanonicalReasoning{Effort: effortFromBudget(budget)}
		}
	}

	return req, nil
}

func GeminiRequestToCanonical(body []byte, urlModel string) (*CanonicalRequest, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini request: %w", err)
	}

	req := &CanonicalRequest{
		Model: stringValue(raw["model"]),
	}
	if req.Model == "" {
		req.Model = urlModel
	}
	req.Instructions = extractGeminiSystemInstruction(raw["systemInstruction"])
	req.Messages = parseGeminiContents(raw["contents"])
	req.Tools = parseGeminiTools(raw["tools"])
	req.ToolChoice = raw["toolConfig"]

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
	}

	if thinking, ok := raw["thinkingConfig"].(map[string]any); ok {
		includeThoughts := boolValue(thinking["includeThoughts"])
		effort := stringValue(thinking["thinkingEffort"])
		req.Thinking = &CanonicalThinking{Enabled: includeThoughts, Effort: effort}
		if includeThoughts {
			req.Reasoning = &CanonicalReasoning{Effort: effort}
		}
	}

	return req, nil
}

func ResponsesRequestToCanonical(body []byte) (*CanonicalRequest, *OpenAIResponsesRequest, error) {
	var req OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("failed to parse Responses request: %w", err)
	}

	canonical := &CanonicalRequest{
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
		canonical.Stream = *req.Stream
	}
	if req.MaxOutputTokens != nil {
		canonical.MaxOutputTokens = int(*req.MaxOutputTokens)
	}
	if req.Reasoning != nil {
		canonical.Reasoning = &CanonicalReasoning{Raw: req.Reasoning}
		if effort := stringValue(req.Reasoning["effort"]); effort != "" {
			canonical.Reasoning.Effort = effort
			canonical.Thinking = &CanonicalThinking{Enabled: true, Effort: effort}
		}
	}
	canonical.ResponseFormat = parseResponsesTextFormat(req.Text)
	canonical.Tools = parseResponsesTools(req.Tools)
	canonical.InputItems, canonical.Messages = parseResponsesInput(req.Input)

	return canonical, &req, nil
}

func CanonicalToOpenAIChatRequest(req *CanonicalRequest) ([]byte, error) {
	out := map[string]any{
		"model":    req.Model,
		"messages": canonicalMessagesToOpenAI(req),
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
		tools, err := canonicalToolsToOpenAI(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = req.ToolChoice
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ResponseFormat != nil {
		out["response_format"] = canonicalResponseFormatToOpenAI(req.ResponseFormat)
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
	return json.Marshal(out)
}

func CanonicalToClaudeRequest(req *CanonicalRequest) ([]byte, error) {
	out := map[string]any{
		"model":      req.Model,
		"messages":   canonicalMessagesToClaude(req),
		"max_tokens": max(req.MaxOutputTokens, ClaudeDefaultMaxTokens),
	}
	if req.Instructions != "" {
		out["system"] = req.Instructions
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
		tools, err := canonicalToolsToClaude(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		budget := req.Thinking.BudgetTokens
		if budget <= 0 {
			budget = budgetFromEffort(req.Thinking.Effort)
		}
		out["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		out["temperature"] = 1.0
		delete(out, "top_p")
	}
	return json.Marshal(out)
}

func CanonicalToGeminiRequest(req *CanonicalRequest) ([]byte, error) {
	out := map[string]any{
		"contents": canonicalMessagesToGemini(req),
	}
	if req.Instructions != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": req.Instructions}},
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
		applyCanonicalResponseFormatToGemini(cfg, req.ResponseFormat)
	}
	if len(cfg) > 0 {
		out["generationConfig"] = cfg
	}
	if len(req.Tools) > 0 {
		tools, err := canonicalToolsToGemini(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		out["toolConfig"] = req.ToolChoice
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		out["thinkingConfig"] = map[string]any{
			"includeThoughts": true,
			"thinkingEffort":  req.Thinking.Effort,
		}
	}
	return json.Marshal(out)
}

// ResponsesPassthroughBody 透传式构造 Responses 上游请求体：以**原始请求字节**为基底，
// 只覆盖 model 名（模型组路由需要），其余字段（input/tools/reasoning/encrypted_content/
// stream/stream_options/prompt_cache_key 等）原样保留。
//
// 为什么不走 CanonicalToResponsesRequest：那条路把请求拆进 canonical 再重建 input，
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

func CanonicalToResponsesRequest(req *CanonicalRequest, original *OpenAIResponsesRequest) ([]byte, error) {
	out := map[string]any{}
	if original != nil {
		b, _ := json.Marshal(original)
		_ = json.Unmarshal(b, &out)
	}

	out["model"] = req.Model
	if instructions := canonicalResponsesInstructions(req); instructions != "" {
		out["instructions"] = instructions
	}
	out["input"] = canonicalInputToResponses(req)
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
		out["tools"] = canonicalToolsToResponses(req.Tools)
	}
	if req.ToolChoice != nil {
		out["tool_choice"] = req.ToolChoice
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ResponseFormat != nil {
		out["text"] = map[string]any{"format": canonicalResponseFormatToResponses(req.ResponseFormat)}
	}
	if req.Reasoning != nil {
		reasoning := map[string]any{}
		for k, v := range req.Reasoning.Raw {
			reasoning[k] = v
		}
		if req.Reasoning.Effort != "" {
			reasoning["effort"] = req.Reasoning.Effort
		}
		out["reasoning"] = reasoning
	}
	return json.Marshal(out)
}

func parseOpenAIChatMessages(raw any) []CanonicalMessage {
	arr, _ := raw.([]any)
	messages := make([]CanonicalMessage, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		msg := CanonicalMessage{
			Role:       stringValue(m["role"]),
			Content:    interfaceToContentParts(m["content"]),
			ToolCallID: stringValue(m["tool_call_id"]),
		}
		if toolCalls, ok := m["tool_calls"].([]any); ok {
			for _, tc := range toolCalls {
				tcm, _ := tc.(map[string]any)
				if tcm == nil {
					continue
				}
				fn, _ := tcm["function"].(map[string]any)
				msg.ToolCalls = append(msg.ToolCalls, CanonicalToolCall{
					ID:        stringValue(tcm["id"]),
					Type:      stringValue(tcm["type"]),
					Name:      stringValue(fn["name"]),
					Arguments: json.RawMessage(stringValue(fn["arguments"])),
				})
			}
		}
		messages = append(messages, msg)
	}
	return messages
}

func parseClaudeMessages(raw any) []CanonicalMessage {
	arr, _ := raw.([]any)
	messages := make([]CanonicalMessage, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		msg := CanonicalMessage{Role: stringValue(m["role"])}
		if blocks, ok := m["content"].([]any); ok {
			for _, block := range blocks {
				bm, _ := block.(map[string]any)
				if bm == nil {
					continue
				}
				switch stringValue(bm["type"]) {
				case "text":
					msg.Content = append(msg.Content, CanonicalContentPart{Type: CanonicalContentText, Text: stringValue(bm["text"]), Raw: bm})
				case "image":
					msg.Content = append(msg.Content, claudeImageBlockToPart(bm))
				case "tool_use":
					inputRaw, _ := json.Marshal(bm["input"])
					msg.ToolCalls = append(msg.ToolCalls, CanonicalToolCall{
						ID:        stringValue(bm["id"]),
						Type:      CanonicalToolFunction,
						Name:      stringValue(bm["name"]),
						Arguments: inputRaw,
					})
				case "tool_result":
					msg.Content = append(msg.Content, CanonicalContentPart{
						Type:       CanonicalContentToolOutput,
						ToolCallID: stringValue(bm["tool_use_id"]),
						ToolOutput: extractTextFromContent(bm["content"]),
						Raw:        bm,
					})
				}
			}
		} else {
			msg.Content = interfaceToContentParts(m["content"])
		}
		messages = append(messages, msg)
	}
	return messages
}

func parseGeminiContents(raw any) []CanonicalMessage {
	arr, _ := raw.([]any)
	messages := make([]CanonicalMessage, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role := stringValue(m["role"])
		if role == "model" {
			role = "assistant"
		}
		msg := CanonicalMessage{Role: role}
		parts, _ := m["parts"].([]any)
		for _, part := range parts {
			pm, _ := part.(map[string]any)
			if pm == nil {
				continue
			}
			if text := stringValue(pm["text"]); text != "" {
				partType := CanonicalContentText
				if boolValue(pm["thought"]) {
					partType = CanonicalContentReasoning
				}
				msg.Content = append(msg.Content, CanonicalContentPart{Type: partType, Text: text, ReasoningText: text, Raw: pm})
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok {
				argsRaw, _ := json.Marshal(fc["args"])
				msg.ToolCalls = append(msg.ToolCalls, CanonicalToolCall{
					ID:        "call_" + stringValue(fc["name"]),
					Type:      CanonicalToolFunction,
					Name:      stringValue(fc["name"]),
					Arguments: argsRaw,
				})
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok {
				respRaw, _ := json.Marshal(fr["response"])
				msg.Content = append(msg.Content, CanonicalContentPart{
					Type:       CanonicalContentToolOutput,
					ToolCallID: stringValue(fr["name"]),
					ToolOutput: string(respRaw),
					Raw:        pm,
				})
			}
			// 多模态：inlineData（base64）/ fileData（URI）→ canonical image part。
			if inline, ok := pm["inlineData"].(map[string]any); ok {
				msg.Content = append(msg.Content, CanonicalContentPart{
					Type:        CanonicalContentImage,
					MediaType:   firstNonEmptyString(stringValue(inline["mimeType"]), stringValue(inline["mime_type"])),
					ImageBase64: stringValue(inline["data"]),
					Raw:         pm,
				})
			}
			if fileData, ok := pm["fileData"].(map[string]any); ok {
				msg.Content = append(msg.Content, CanonicalContentPart{
					Type:      CanonicalContentImage,
					MediaType: firstNonEmptyString(stringValue(fileData["mimeType"]), stringValue(fileData["mime_type"])),
					ImageURL:  firstNonEmptyString(stringValue(fileData["fileUri"]), stringValue(fileData["file_uri"])),
					Raw:       pm,
				})
			}
		}
		messages = append(messages, msg)
	}
	return messages
}

func parseResponsesInput(raw json.RawMessage) ([]CanonicalInputItem, []CanonicalMessage) {
	if len(raw) == 0 {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		item := CanonicalInputItem{
			Type:    CanonicalInputMessage,
			Role:    "user",
			Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: text}},
		}
		return []CanonicalInputItem{item}, []CanonicalMessage{{Role: "user", Content: item.Content}}
	}

	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, nil
	}

	items := make([]CanonicalInputItem, 0, len(arr))
	messages := make([]CanonicalMessage, 0, len(arr))
	for _, entry := range arr {
		m, _ := entry.(map[string]any)
		if m == nil {
			continue
		}
		itemType := stringValue(m["type"])
		if itemType == "" && m["role"] != nil {
			itemType = CanonicalInputMessage
		}
		item := CanonicalInputItem{Type: itemType, Role: stringValue(m["role"]), ItemID: stringValue(m["id"])}
		switch itemType {
		case CanonicalInputMessage:
			item.Type = CanonicalInputMessage
			item.Content = interfaceToContentParts(m["content"])
			if item.Role == "" {
				item.Role = "user"
			}
			messages = append(messages, CanonicalMessage{Role: item.Role, Content: item.Content})
		case CanonicalInputFunctionCallOutput:
			item.Type = CanonicalInputFunctionCallOutput
			item.CallID = stringValue(m["call_id"])
			item.Output = stringValue(m["output"])
			messages = append(messages, CanonicalMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    []CanonicalContentPart{{Type: CanonicalContentText, Text: item.Output}},
			})
		default:
			item.RawExtra = map[string]json.RawMessage{}
			rawEntry, _ := json.Marshal(m)
			item.RawExtra["raw"] = rawEntry
		}
		items = append(items, item)
	}
	return items, messages
}

func parseOpenAIChatTools(raw any) []CanonicalTool {
	arr, _ := raw.([]any)
	tools := make([]CanonicalTool, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if stringValue(m["type"]) == "function" {
			fn, _ := m["function"].(map[string]any)
			tools = append(tools, CanonicalTool{
				Type:        CanonicalToolFunction,
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
				Raw:         m,
			})
		}
	}
	return tools
}

func parseClaudeTools(raw any) []CanonicalTool {
	arr, _ := raw.([]any)
	tools := make([]CanonicalTool, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		tools = append(tools, CanonicalTool{
			Type:        CanonicalToolFunction,
			Name:        stringValue(m["name"]),
			Description: stringValue(m["description"]),
			Parameters:  mapValue(m["input_schema"]),
			Raw:         m,
		})
	}
	return tools
}

func parseGeminiTools(raw any) []CanonicalTool {
	arr, _ := raw.([]any)
	var tools []CanonicalTool
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
			tools = append(tools, CanonicalTool{
				Type:        CanonicalToolFunction,
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
				Raw:         fn,
			})
		}
	}
	return tools
}

func parseResponsesTools(raw []map[string]any) []CanonicalTool {
	tools := make([]CanonicalTool, 0, len(raw))
	for _, tool := range raw {
		t := stringValue(tool["type"])
		ct := CanonicalTool{Type: t, Raw: tool}
		if t == CanonicalToolFunction {
			ct.Name = stringValue(tool["name"])
			ct.Description = stringValue(tool["description"])
			ct.Parameters = mapValue(tool["parameters"])
		}
		if t == CanonicalToolWebSearchPreview {
			ct.SearchContextSize = stringValue(tool["search_context_size"])
		}
		if t == CanonicalToolFileSearch {
			if ids, ok := tool["vector_store_ids"].([]any); ok {
				for _, id := range ids {
					ct.VectorStoreIDs = append(ct.VectorStoreIDs, fmt.Sprintf("%v", id))
				}
			}
		}
		tools = append(tools, ct)
	}
	return tools
}

func canonicalMessagesToOpenAI(req *CanonicalRequest) []map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if req.Instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.Instructions})
	}
	for _, msg := range req.Messages {
		out := map[string]any{
			"role":    msg.Role,
			"content": contentPartsToInterface(msg.Content),
		}
		if msg.ToolCallID != "" {
			out["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			var calls []map[string]any
			for _, call := range msg.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": string(call.Arguments),
					},
				})
			}
			out["tool_calls"] = calls
		}
		messages = append(messages, out)
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

// claudeImageBlockToPart 把 Claude image block（{"source":{...}}）解析为 canonical
// image part：base64 source → ImageBase64+MediaType；url source → ImageURL。
func claudeImageBlockToPart(bm map[string]any) CanonicalContentPart {
	part := CanonicalContentPart{Type: CanonicalContentImage, Raw: bm}
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
func imagePartBase64(part CanonicalContentPart) (string, string) {
	if part.ImageBase64 != "" {
		return part.MediaType, part.ImageBase64
	}
	if strings.HasPrefix(part.ImageURL, "data:") {
		if mt, b64, ok := parseDataURL(part.ImageURL); ok {
			return mt, b64
		}
	}
	return "", ""
}

// imagePartToOpenAIURL 把图片 part 渲染为 OpenAI image_url 的 url 值
// （http(s) URL 原样；base64 数据组装成 data: URI）。
func imagePartToOpenAIURL(part CanonicalContentPart) string {
	if part.ImageURL != "" {
		return part.ImageURL
	}
	if part.ImageBase64 != "" {
		mt := part.MediaType
		if mt == "" {
			mt = "image/png"
		}
		return "data:" + mt + ";base64," + part.ImageBase64
	}
	return ""
}

// imagePartToClaudeSource 把图片 part 渲染为 Claude image block 的 source。
func imagePartToClaudeSource(part CanonicalContentPart) map[string]any {
	if mt, b64 := imagePartBase64(part); b64 != "" {
		if mt == "" {
			mt = "image/png"
		}
		return map[string]any{"type": "base64", "media_type": mt, "data": b64}
	}
	if part.ImageURL != "" {
		return map[string]any{"type": "url", "url": part.ImageURL}
	}
	return nil
}

// imagePartToGeminiPart 把图片 part 渲染为 Gemini 的 inlineData（base64）或
// fileData（http(s) URL）part。
func imagePartToGeminiPart(part CanonicalContentPart) map[string]any {
	if mt, b64 := imagePartBase64(part); b64 != "" {
		if mt == "" {
			mt = "image/png"
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mt, "data": b64}}
	}
	if part.ImageURL != "" {
		fileData := map[string]any{"fileUri": part.ImageURL}
		if part.MediaType != "" {
			fileData["mimeType"] = part.MediaType
		}
		return map[string]any{"fileData": fileData}
	}
	return nil
}

func canonicalMessagesToClaude(req *CanonicalRequest) []map[string]any {
	var messages []map[string]any
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "assistant"
		}
		var content []map[string]any
		for _, part := range msg.Content {
			switch part.Type {
			case CanonicalContentText:
				content = append(content, map[string]any{"type": "text", "text": part.Text})
			case CanonicalContentImage:
				if src := imagePartToClaudeSource(part); src != nil {
					content = append(content, map[string]any{"type": "image", "source": src})
				}
			case CanonicalContentToolOutput:
				content = append(content, map[string]any{"type": "tool_result", "tool_use_id": part.ToolCallID, "content": part.ToolOutput})
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
			content = []map[string]any{{"type": "text", "text": ""}}
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}
	return messages
}

func canonicalMessagesToGemini(req *CanonicalRequest) []map[string]any {
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
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		for _, part := range msg.Content {
			switch part.Type {
			case CanonicalContentText:
				parts = append(parts, map[string]any{"text": part.Text})
			case CanonicalContentImage:
				if p := imagePartToGeminiPart(part); p != nil {
					parts = append(parts, p)
				}
			case CanonicalContentReasoning:
				parts = append(parts, map[string]any{"text": part.ReasoningText, "thought": true})
			case CanonicalContentToolOutput:
				// Gemini 的 functionResponse.response 必须是 JSON 对象（google.protobuf.Struct），
				// 不能是字符串、数组、null 或空。三层降级策略（参考 new-api）：
				// 1. 尝试解析为 JSON 对象 → 直接使用
				// 2. 解析为 JSON 数组 → 包装为 {"result": array}
				// 3. 空或非 JSON 文本 → 包装为 {"content": text}
				var responseMap map[string]any
				if part.ToolOutput != "" {
					if err := json.Unmarshal([]byte(part.ToolOutput), &responseMap); err != nil {
						var arr []any
						if err := json.Unmarshal([]byte(part.ToolOutput), &arr); err == nil {
							responseMap = map[string]any{"result": arr}
						} else {
							responseMap = map[string]any{"content": part.ToolOutput}
						}
					}
				} else {
					responseMap = map[string]any{"content": ""}
				}

				// functionResponse.name 必须是函数名，而非 tool_use_id；回查之前的 tool_use 获取函数名。
				name := part.ToolCallID
				if fn, ok := toolCallNames[part.ToolCallID]; ok {
					name = fn
				}

				parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": name, "response": responseMap}})
			}
		}
		for _, call := range msg.ToolCalls {
			var args any = map[string]any{}
			if len(call.Arguments) > 0 {
				_ = json.Unmarshal(call.Arguments, &args)
			}
			parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Name, "args": args}})
		}
		if len(parts) == 0 {
			parts = []map[string]any{{"text": ""}}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	return contents
}

func canonicalResponsesInstructions(req *CanonicalRequest) string {
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
		if text := strings.TrimSpace(canonicalText(msg.Content)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func canonicalInputToResponses(req *CanonicalRequest) any {
	if len(req.InputItems) > 0 {
		var items []map[string]any
		for _, item := range req.InputItems {
			switch item.Type {
			case CanonicalInputFunctionCallOutput:
				items = append(items, map[string]any{"type": "function_call_output", "call_id": item.CallID, "output": item.Output})
			default:
				if raw := rawResponsesInputItem(item.RawExtra); raw != nil {
					items = append(items, raw)
					continue
				}
				role := responsesInputRole(item.Role)
				items = append(items, map[string]any{"role": role, "content": canonicalContentToResponsesInputContent(role, item.Content)})
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
			output := canonicalText(msg.Content)
			if callID == "" {
				items = append(items, map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("[tool_output_missing_call_id] %s", output)}}})
				continue
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": callID, "output": output})
			continue
		}

		items = append(items, map[string]any{"role": responsesInputRole(role), "content": canonicalContentToResponsesInputContent(role, msg.Content)})
		if role == "assistant" {
			items = append(items, canonicalToolCallsToResponsesItems(msg.ToolCalls)...)
		}
	}
	return items
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

func canonicalToolCallsToResponsesItems(calls []CanonicalToolCall) []map[string]any {
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

func canonicalContentToResponsesInputContent(role string, parts []CanonicalContentPart) []map[string]any {
	role = responsesInputRole(role)
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case CanonicalContentText:
			out = append(out, map[string]any{"type": responsesTextPartTypeForRole(role), "text": part.Text})
		case CanonicalContentReasoning:
			if role == "assistant" {
				text := part.ReasoningText
				if text == "" {
					text = part.Text
				}
				out = append(out, map[string]any{"type": "output_text", "text": text})
			}
		case CanonicalContentImage:
			if role == "user" {
				out = append(out, map[string]any{"type": "input_image", "image_url": part.ImageURL})
			}
		case CanonicalContentFile:
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
				out = append(out, item)
			}
		default:
			if refusal := refusalTextFromRaw(part.Raw); role == "assistant" && refusal != "" {
				out = append(out, map[string]any{"type": "refusal", "refusal": refusal})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": responsesTextPartTypeForRole(role), "text": ""})
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

func canonicalToolsToOpenAI(tools []CanonicalTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != CanonicalToolFunction {
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to OpenAI chat completions", tool.Type)
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return out, nil
}

func canonicalToolsToClaude(tools []CanonicalTool) ([]map[string]any, error) {
	var out []map[string]any
	for _, tool := range tools {
		if tool.Type != CanonicalToolFunction {
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to Claude messages", tool.Type)
		}
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.Parameters,
		})
	}
	return out, nil
}

func canonicalToolsToGemini(tools []CanonicalTool) ([]map[string]any, error) {
	var declarations []map[string]any
	for _, tool := range tools {
		if tool.Type != CanonicalToolFunction {
			return nil, fmt.Errorf("builtin tool %q cannot be transformed to Gemini", tool.Type)
		}
		declarations = append(declarations, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		})
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	return []map[string]any{{"functionDeclarations": declarations}}, nil
}

func canonicalToolsToResponses(tools []CanonicalTool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Raw != nil {
			out = append(out, tool.Raw)
			continue
		}
		m := map[string]any{"type": tool.Type}
		if tool.Type == CanonicalToolFunction {
			m["name"] = tool.Name
			m["description"] = tool.Description
			m["parameters"] = tool.Parameters
		}
		out = append(out, m)
	}
	return out
}

func parseOpenAIResponseFormat(raw any) *CanonicalResponseFormat {
	m, _ := raw.(map[string]any)
	if m == nil {
		return nil
	}
	f := &CanonicalResponseFormat{Type: stringValue(m["type"]), Raw: m}
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

func parseResponsesTextFormat(raw map[string]any) *CanonicalResponseFormat {
	if raw == nil {
		return nil
	}
	format, _ := raw["format"].(map[string]any)
	if format == nil {
		return nil
	}
	return &CanonicalResponseFormat{
		Type:        stringValue(format["type"]),
		Name:        stringValue(format["name"]),
		Description: stringValue(format["description"]),
		Schema:      mapValue(format["schema"]),
		Raw:         format,
	}
}

func parseGeminiResponseFormat(cfg map[string]any) *CanonicalResponseFormat {
	mime := stringValue(cfg["responseMimeType"])
	schema := mapValue(cfg["responseSchema"])
	if mime == "" && schema == nil {
		return nil
	}
	formatType := "text"
	if strings.Contains(mime, "json") {
		formatType = "json_schema"
	}
	return &CanonicalResponseFormat{Type: formatType, Schema: schema, Raw: cfg}
}

func canonicalResponseFormatToOpenAI(f *CanonicalResponseFormat) map[string]any {
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

func canonicalResponseFormatToResponses(f *CanonicalResponseFormat) map[string]any {
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

func applyCanonicalResponseFormatToGemini(cfg map[string]any, f *CanonicalResponseFormat) {
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
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func effortFromBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	if budget <= EffortBudgetLowCeil {
		return "low"
	}
	if budget >= EffortBudgetHighFloor {
		return "high"
	}
	return "medium"
}

func budgetFromEffort(effort string) int {
	switch effort {
	case "low":
		return 1280
	case "high":
		return EffortBudgetHighFloor
	default:
		return EffortBudgetDefault
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func OpenAIChatResponseToCanonical(resp *OpenAIResponse) (*CanonicalResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil OpenAI response")
	}
	out := &CanonicalResponse{
		ID:        resp.ID,
		Model:     resp.Model,
		CreatedAt: resp.Created,
		Status:    "completed",
		Usage:     canonicalUsageFromOpenAIUsage(resp.Usage),
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		item := CanonicalOutputItem{
			ID:      newCanonicalResponseID("msg"),
			Type:    CanonicalOutputMessage,
			Status:  "completed",
			Role:    choice.Message.Role,
			Content: interfaceToContentParts(choice.Message.Content),
		}
		if item.Role == "" {
			item.Role = "assistant"
		}
		out.Output = append(out.Output, item)
		for _, call := range choice.Message.ToolCalls {
			out.Output = append(out.Output, CanonicalOutputItem{
				ID:        call.ID,
				Type:      CanonicalOutputFunctionCall,
				Status:    "completed",
				CallID:    call.ID,
				Name:      call.Function.Name,
				Arguments: json.RawMessage(call.Function.Arguments),
			})
		}
		out.StopReason = choice.FinishReason
	}
	return out, nil
}

func ClaudeResponseToCanonical(resp *ClaudeResponse) (*CanonicalResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Claude response")
	}
	out := &CanonicalResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		CreatedAt:  time.Now().Unix(),
		Status:     "completed",
		StopReason: resp.StopReason,
		Usage:      canonicalUsageFromClaudeUsage(resp.Usage),
	}
	msg := CanonicalOutputItem{ID: resp.ID, Type: CanonicalOutputMessage, Status: "completed", Role: "assistant"}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, CanonicalContentPart{Type: CanonicalContentText, Text: block.Text})
		case "thinking":
			out.Output = append(out.Output, CanonicalOutputItem{
				ID:      newCanonicalResponseID("rs"),
				Type:    CanonicalOutputReasoning,
				Status:  "completed",
				Content: []CanonicalContentPart{{Type: CanonicalContentReasoning, ReasoningText: block.Thinking, Text: block.Thinking}},
			})
		case "tool_use":
			out.Output = append(out.Output, CanonicalOutputItem{
				ID:        block.ID,
				Type:      CanonicalOutputFunctionCall,
				Status:    "completed",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}
	if len(msg.Content) > 0 {
		out.Output = append([]CanonicalOutputItem{msg}, out.Output...)
	}
	return out, nil
}

func GeminiResponseToCanonical(resp *GeminiResponse) (*CanonicalResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Gemini response")
	}
	out := &CanonicalResponse{
		ID:        newCanonicalResponseID("gemini"),
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Usage:     canonicalUsageFromGeminiUsage(resp.UsageMetadata),
	}
	msg := CanonicalOutputItem{ID: newCanonicalResponseID("msg"), Type: CanonicalOutputMessage, Status: "completed", Role: "assistant"}
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		out.StopReason = cand.FinishReason
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				msg.Content = append(msg.Content, CanonicalContentPart{Type: CanonicalContentText, Text: part.Text})
			}
			if part.FunctionCall != nil {
				raw, _ := json.Marshal(part.FunctionCall)
				out.Output = append(out.Output, CanonicalOutputItem{
					ID:        newCanonicalResponseID("call"),
					Type:      CanonicalOutputFunctionCall,
					Status:    "completed",
					Arguments: raw,
				})
			}
		}
	}
	if len(msg.Content) > 0 {
		out.Output = append([]CanonicalOutputItem{msg}, out.Output...)
	}
	return out, nil
}

func ResponsesResponseToCanonical(resp *OpenAIResponsesResponse) (*CanonicalResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil Responses response")
	}
	out := &CanonicalResponse{
		ID:        resp.ID,
		Model:     resp.Model,
		CreatedAt: resp.CreatedAt,
		Status:    resp.Status,
		Usage:     canonicalUsageFromResponsesUsage(resp.Usage),
	}
	for _, item := range resp.Output {
		citem := CanonicalOutputItem{
			ID:        item.ID,
			Type:      item.Type,
			Status:    item.Status,
			Role:      item.Role,
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Raw:       map[string]any{"quality": item.Quality, "size": item.Size},
		}
		for _, content := range item.Content {
			if content.Type == "output_text" || content.Type == "text" {
				citem.Content = append(citem.Content, CanonicalContentPart{Type: CanonicalContentText, Text: content.Text})
			}
		}
		for _, summary := range item.Summary {
			citem.Summary = append(citem.Summary, CanonicalReasoningSummary{Type: summary.Type, Text: summary.Text})
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

func CanonicalToOpenAIChatResponse(resp *CanonicalResponse) (*OpenAIResponse, error) {
	msg := Message{Role: "assistant", Content: ""}
	var toolCalls []OpenAIToolCall
	var text strings.Builder
	for _, item := range resp.Output {
		switch item.Type {
		case CanonicalOutputMessage:
			text.WriteString(canonicalText(item.Content))
		case CanonicalOutputFunctionCall:
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: OpenAIToolFunction{
					Name:      item.Name,
					Arguments: string(item.Arguments),
				},
			})
		}
	}
	msg.Content = text.String()
	msg.ToolCalls = toolCalls
	return &OpenAIResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: resp.CreatedAt,
		Model:   resp.Model,
		Choices: []Choice{{Index: 0, Message: msg, FinishReason: canonicalStopToOpenAI(resp.StopReason)}},
		Usage:   openAIUsageFromCanonical(resp.Usage),
	}, nil
}

func CanonicalToClaudeResponse(resp *CanonicalResponse) (*ClaudeResponse, error) {
	var content []ClaudeContent
	for _, item := range resp.Output {
		switch item.Type {
		case CanonicalOutputMessage:
			if text := canonicalText(item.Content); text != "" {
				content = append(content, ClaudeContent{Type: "text", Text: text})
			}
		case CanonicalOutputReasoning:
			content = append(content, ClaudeContent{Type: "thinking", Thinking: canonicalText(item.Content)})
		case CanonicalOutputFunctionCall:
			content = append(content, ClaudeContent{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: item.Arguments})
		}
	}
	if len(content) == 0 {
		content = []ClaudeContent{{Type: "text", Text: ""}}
	}
	return &ClaudeResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      resp.Model,
		StopReason: canonicalStopToClaude(resp.StopReason),
		Usage:      claudeUsageFromCanonical(resp.Usage),
	}, nil
}

func CanonicalToGeminiResponse(resp *CanonicalResponse) (*GeminiResponse, error) {
	var parts []GeminiPart
	for _, item := range resp.Output {
		switch item.Type {
		case CanonicalOutputMessage:
			if text := canonicalText(item.Content); text != "" {
				parts = append(parts, GeminiPart{Text: text})
			}
		case CanonicalOutputFunctionCall:
			parts = append(parts, GeminiPart{FunctionCall: map[string]any{"name": item.Name, "args": jsonRawToAny(item.Arguments)}})
		}
	}
	if len(parts) == 0 {
		parts = []GeminiPart{{Text: ""}}
	}
	return &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content:      GeminiContent{Role: "model", Parts: parts},
			FinishReason: canonicalStopToGemini(resp.StopReason),
		}},
		UsageMetadata: geminiUsageFromCanonical(resp.Usage),
	}, nil
}

func CanonicalToResponsesResponse(resp *CanonicalResponse) (*OpenAIResponsesResponse, error) {
	out := &OpenAIResponsesResponse{
		ID:        resp.ID,
		Object:    "response",
		CreatedAt: resp.CreatedAt,
		Status:    resp.Status,
		Model:     resp.Model,
		Usage:     responsesUsageFromCanonical(resp.Usage),
	}
	if out.ID == "" {
		out.ID = newCanonicalResponseID("resp")
	}
	if out.CreatedAt == 0 {
		out.CreatedAt = time.Now().Unix()
	}
	if out.Status == "" {
		out.Status = "completed"
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
		}
		if ritem.Type == CanonicalOutputMessage || ritem.Type == "message" {
			ritem.Type = "message"
			ritem.Role = "assistant"
			ritem.Content = []ResponsesOutputContent{{Type: "output_text", Text: canonicalText(item.Content)}}
		}
		if ritem.Type == CanonicalOutputFunctionCall {
			ritem.Type = "function_call"
		}
		if ritem.Type == CanonicalOutputReasoning {
			ritem.Type = "reasoning"
			for _, s := range item.Summary {
				ritem.Summary = append(ritem.Summary, ResponsesReasoningSummaryPart{Type: s.Type, Text: s.Text})
			}
		}
		out.Output = append(out.Output, ritem)
	}
	return out, nil
}

func canonicalUsageFromOpenAIUsage(usage Usage) *CanonicalUsage {
	u := &CanonicalUsage{
		InputTokens:       usage.PromptTokens,
		OutputTokens:      usage.CompletionTokens,
		TotalTokens:       usage.TotalTokens,
		CachedInputTokens: max(usage.CachedTokens, usage.PromptCacheHitTokens),
		ReasoningTokens:   usage.CompletionTokensDetails.ReasoningTokens,
		Source:            "provider_response",
	}
	if u.CachedInputTokens == 0 {
		u.CachedInputTokens = max(usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheReadTokens)
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

func canonicalUsageFromClaudeUsage(usage ClaudeUsage) *CanonicalUsage {
	input := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if usage.CacheCreation != nil {
		input += usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
	}
	u := &CanonicalUsage{
		InputTokens:              input,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              input + usage.OutputTokens,
		CachedInputTokens:        usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		Source:                   "provider_response",
	}
	if usage.ServerToolUse != nil {
		u.WebSearchCallCount = usage.ServerToolUse.WebSearchRequests
	}
	return u
}

func canonicalUsageFromGeminiUsage(usage GeminiUsageMeta) *CanonicalUsage {
	u := &CanonicalUsage{
		InputTokens:       usage.PromptTokenCount + usage.ToolUsePromptTokenCount,
		OutputTokens:      usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
		TotalTokens:       usage.TotalTokenCount,
		CachedInputTokens: usage.CachedContentTokenCount,
		ReasoningTokens:   usage.ThoughtsTokenCount,
		ToolUseTokens:     usage.ToolUsePromptTokenCount,
		Source:            "provider_response",
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

func canonicalUsageFromResponsesUsage(usage *ResponsesUsage) *CanonicalUsage {
	if usage == nil {
		return nil
	}
	u := &CanonicalUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		Source:       "provider_response",
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

func openAIUsageFromCanonical(u *CanonicalUsage) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		CachedTokens:     u.CachedInputTokens,
		PromptTokensDetails: PromptTokensDetails{
			CachedTokens: u.CachedInputTokens,
		},
		CompletionTokensDetails: CompletionTokensDetails{
			ReasoningTokens: u.ReasoningTokens,
		},
	}
}

func claudeUsageFromCanonical(u *CanonicalUsage) ClaudeUsage {
	if u == nil {
		return ClaudeUsage{}
	}
	return ClaudeUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CachedInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
}

func geminiUsageFromCanonical(u *CanonicalUsage) GeminiUsageMeta {
	if u == nil {
		return GeminiUsageMeta{}
	}
	return GeminiUsageMeta{
		PromptTokenCount:        u.InputTokens,
		ToolUsePromptTokenCount: u.ToolUseTokens,
		CandidatesTokenCount:    u.OutputTokens,
		TotalTokenCount:         valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		ThoughtsTokenCount:      u.ReasoningTokens,
		CachedContentTokenCount: u.CachedInputTokens,
	}
}

func responsesUsageFromCanonical(u *CanonicalUsage) *ResponsesUsage {
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

func canonicalStopToOpenAI(reason string) string {
	switch reason {
	case "end_turn", "STOP", "":
		return "stop"
	case "max_tokens", "MAX_TOKENS":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func canonicalStopToClaude(reason string) string {
	switch reason {
	case "stop", "STOP", "":
		return "end_turn"
	case "length", "MAX_TOKENS":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return reason
	}
}

func canonicalStopToGemini(reason string) string {
	switch reason {
	case "stop", "end_turn", "":
		return "STOP"
	case "length", "max_tokens":
		return "MAX_TOKENS"
	default:
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
