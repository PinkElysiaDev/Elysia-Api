package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Platform 平台类型
type Platform string

const (
	PlatformOpenAI    Platform = "openai"
	PlatformDeepSeek  Platform = "deepseek"
	PlatformAnthropic Platform = "anthropic"
	PlatformGemini    Platform = "gemini"
	PlatformAzure     Platform = "azure"
	PlatformUnknown   Platform = "unknown"
)

// DetectPlatform 从 baseURL 或 platform 字段检测平台类型
func DetectPlatform(baseURL, platform string) Platform {
	// 首先检查明确的 platform 字段
	switch strings.ToLower(platform) {
	case "openai":
		return PlatformOpenAI
	case "deepseek":
		return PlatformDeepSeek
	case "anthropic", "claude":
		return PlatformAnthropic
	case "gemini", "google":
		return PlatformGemini
	case "azure":
		return PlatformAzure
	}

	// 从 baseURL 检测
	lowerURL := strings.ToLower(baseURL)
	if strings.Contains(lowerURL, "deepseek") {
		return PlatformDeepSeek
	}
	if strings.Contains(lowerURL, "anthropic") || strings.Contains(lowerURL, "claude") {
		return PlatformAnthropic
	}
	if strings.Contains(lowerURL, "gemini") || strings.Contains(lowerURL, "google") {
		return PlatformGemini
	}
	if strings.Contains(lowerURL, "azure") || strings.Contains(lowerURL, "openai.azure") {
		return PlatformAzure
	}
	if strings.Contains(lowerURL, "openai") {
		return PlatformOpenAI
	}

	return PlatformUnknown
}

// FormatType 请求格式类型
type FormatType string

const (
	FormatOpenAI    FormatType = "openai"
	FormatDeepSeek  FormatType = "deepseek"
	FormatGemini    FormatType = "gemini"
	FormatClaude    FormatType = "claude"
	FormatUnknown   FormatType = "unknown"
)

// DetectInputFormat 检测输入请求的格式
func DetectInputFormat(body []byte) FormatType {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return FormatUnknown
	}

	// 检查 Gemini 特有字段
	if _, hasContents := req["contents"]; hasContents {
		return FormatGemini
	}

	// 检查 Claude 特有字段
	if _, hasSystem := req["system"]; hasSystem {
		if _, hasMaxTokens := req["max_tokens"]; hasMaxTokens {
			return FormatClaude
		}
	}

	// 默认为 OpenAI 格式
	return FormatOpenAI
}

// UnifiedRequest 统一的内部请求格式
// 这是所有格式的"全集"，包含所有可能的字段
type UnifiedRequest struct {
	// 基础字段
	Model               string               `json:"model"`
	Messages            []UnifiedMessage    `json:"messages"`
	MaxTokens           int                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Temperature         *float64             `json:"temperature,omitempty"`
	TopP                *float64             `json:"top_p,omitempty"`
	TopK                int                  `json:"top_k,omitempty"`
	N                   int                  `json:"n,omitempty"`
	Stream              bool                 `json:"stream,omitempty"`
	StreamOptions       *StreamOptions       `json:"stream_options,omitempty"`
	Stop                interface{}          `json:"stop,omitempty"`

	// 惩罚参数
	PresencePenalty     *float64             `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64             `json:"frequency_penalty,omitempty"`

	// 思考模式相关 (OpenAI/Claude/Gemini)
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	ThinkingConfig      *ThinkingConfig      `json:"thinking_config,omitempty"`

	// 工具调用
	Tools               []Tool               `json:"tools,omitempty"`
	ToolChoice          interface{}          `json:"tool_choice,omitempty"`
	ParallelToolCalls   bool                 `json:"parallel_tool_calls,omitempty"`

	// 响应格式
	ResponseFormat      *ResponseFormat      `json:"response_format,omitempty"`

	// 其他常用字段
	User                string               `json:"user,omitempty"`
	Seed                float64              `json:"seed,omitempty"`
	LogProbs            bool                 `json:"logprobs,omitempty"`
	TopLogProbs         int                  `json:"top_logprobs,omitempty"`

	// SiliconFlow / 其他提供商特定字段
	PromptCacheKey      string               `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage      `json:"prompt_cache_retention,omitempty"`

	// 预留扩展字段（使用 json.RawMessage 保留原始 JSON）
	ExtraFields         map[string]json.RawMessage `json:"-"`
}

type UnifiedMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ThinkingConfig struct {
	Enabled bool   `json:"enabled"`
	Effort  string `json:"effort,omitempty"` // "low" | "medium" | "high"
}

// ConvertToUnified 将任意格式的请求转换为统一格式
func ConvertToUnified(body []byte) (*UnifiedRequest, error) {
	format := DetectInputFormat(body)

	switch format {
	case FormatGemini:
		return GeminiToUnified(body)
	case FormatClaude:
		return ClaudeToUnified(body)
	case FormatOpenAI, FormatDeepSeek, FormatUnknown:
		return OpenAIToUnified(body)
	default:
		return OpenAIToUnified(body)
	}
}

// OpenAIToUnified 将 OpenAI 格式转换为统一格式
func OpenAIToUnified(body []byte) (*UnifiedRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI request: %w", err)
	}

	unified := &UnifiedRequest{
		Model: reqString(req, "model"),
		Stream: reqBool(req, "stream"),
	}

	// 解析 messages
	if msgs, ok := req["messages"].([]interface{}); ok {
		for _, msg := range msgs {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				unified.Messages = append(unified.Messages, UnifiedMessage{
					Role:    reqString(msgMap, "role"),
					Content: msgMap["content"],
				})
			}
		}
	}

	// 数值字段
	if v, ok := req["max_tokens"].(float64); ok {
		unified.MaxTokens = int(v)
	}
	if v, ok := req["max_completion_tokens"].(float64); ok {
		unified.MaxCompletionTokens = int(v)
	}
	if v, ok := req["temperature"].(float64); ok {
		unified.Temperature = &v
	}
	if v, ok := req["top_p"].(float64); ok {
		unified.TopP = &v
	}
	if v, ok := req["top_k"].(float64); ok {
		unified.TopK = int(v)
	}
	if v, ok := req["n"].(float64); ok {
		unified.N = int(v)
	}
	if v, ok := req["presence_penalty"].(float64); ok {
		unified.PresencePenalty = &v
	}
	if v, ok := req["frequency_penalty"].(float64); ok {
		unified.FrequencyPenalty = &v
	}

	// 其他字段
	unified.Stop = req["stop"]
	unified.ToolChoice = req["tool_choice"]
	if req["user"] != nil {
		unified.User = reqString(req, "user")
	}

	// stream_options
	if so, ok := req["stream_options"].(map[string]interface{}); ok {
		if include, ok := so["include_usage"].(bool); ok {
			unified.StreamOptions = &StreamOptions{IncludeUsage: include}
		}
	}

	// prompt_cache_key
	if req["prompt_cache_key"] != nil {
		unified.PromptCacheKey = reqString(req, "prompt_cache_key")
	}

	// reasoning_effort
	if reasoningEffort := reqString(req, "reasoning_effort"); reasoningEffort != "" {
		unified.ReasoningEffort = reasoningEffort
	}

	// Tools 解析（简化处理）
	if tools, ok := req["tools"].([]interface{}); ok {
		for _, tool := range tools {
			if toolMap, ok := tool.(map[string]interface{}); ok {
				if toolMap["type"] == "function" {
					if funcMap, ok := toolMap["function"].(map[string]interface{}); ok {
						var params map[string]interface{}
						if p, ok := funcMap["parameters"].(map[string]interface{}); ok {
							params = p
						}
						unified.Tools = append(unified.Tools, Tool{
							Type: "function",
							Function: FunctionDefinition{
								Name:       reqString(funcMap, "name"),
								Description: reqString(funcMap, "description"),
								Parameters:  params,
							},
						})
					}
				}
			}
		}
	}

	return unified, nil
}

// Helper function to safely get string value from map
func reqString(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Helper function to safely get bool value from map
func reqBool(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// GeminiToUnified 将 Gemini 格式转换为统一格式
func GeminiToUnified(body []byte) (*UnifiedRequest, error) {
	var geminiReq struct {
		Model         string `json:"model"`
		Contents      []GeminiContent `json:"contents"`
		GenerationConfig struct {
			Temperature float64 `json:"temperature,omitempty"`
			MaxTokens   int     `json:"maxOutputTokens,omitempty"`
			TopP        float64 `json:"topP,omitempty"`
		} `json:"generationConfig,omitempty"`
		ThinkingConfig *struct {
			IncludeThoughts bool   `json:"includeThoughts,omitempty"`
			ThinkingEffort  string `json:"thinkingEffort,omitempty"` // "low" | "medium" | "high"
		} `json:"thinkingConfig,omitempty"`
	}

	if err := json.Unmarshal(body, &geminiReq); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini request: %w", err)
	}

	unified := &UnifiedRequest{
		Model:     geminiReq.Model,
		MaxTokens: geminiReq.GenerationConfig.MaxTokens,
	}

	// 正确处理指针类型
	if geminiReq.GenerationConfig.Temperature > 0 {
		unified.Temperature = &geminiReq.GenerationConfig.Temperature
	}
	if geminiReq.GenerationConfig.TopP > 0 {
		unified.TopP = &geminiReq.GenerationConfig.TopP
	}

	// 转换思考配置
	if geminiReq.ThinkingConfig != nil && geminiReq.ThinkingConfig.IncludeThoughts {
		unified.ThinkingConfig = &ThinkingConfig{
			Enabled: true,
			Effort:  geminiReq.ThinkingConfig.ThinkingEffort,
		}
	}

	// 转换消息
	for _, content := range geminiReq.Contents {
		role := content.Role
		if role == "user" {
			role = "user"
		} else if role == "model" {
			role = "assistant"
		}

		// 处理 parts
		var contentParts []interface{}
		var textContent strings.Builder

		for _, part := range content.Parts {
			if part.Text != "" {
				// 如果只有文本，合并为单一字符串
				contentParts = nil
				textContent.WriteString(part.Text)
			}
			if part.ExecutableCode != nil {
				contentParts = append(contentParts, map[string]interface{}{
					"type": "code",
					"code": part.ExecutableCode.Code,
				})
			}
		}

		var finalContent interface{}
		if len(contentParts) > 0 && textContent.Len() > 0 {
			// 混合内容
			finalContent = append([]interface{}{
				map[string]interface{}{"type": "text", "text": textContent.String()},
			}, contentParts...)
		} else if len(contentParts) > 0 {
			finalContent = contentParts
		} else {
			finalContent = textContent.String()
		}

		unified.Messages = append(unified.Messages, UnifiedMessage{
			Role:    role,
			Content: finalContent,
		})
	}

	return unified, nil
}

// ClaudeToUnified 将 Claude 格式转换为统一格式
func ClaudeToUnified(body []byte) (*UnifiedRequest, error) {
	var claudeReq struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content interface{} `json:"content"`
		} `json:"messages"`
		System          string  `json:"system,omitempty"`
		Temperature     float64 `json:"temperature,omitempty"`
		TopP            float64 `json:"top_p,omitempty"`
		Stream          bool    `json:"stream,omitempty"`
		Stop            interface{} `json:"stop,omitempty"`
		ThinkingEnabled *bool   `json:"thinking_enabled,omitempty"`
		ThinkingBudget  int     `json:"thinking_budget,omitempty"`
		Tools           []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description,omitempty"`
			InputSchema map[string]interface{} `json:"input_schema,omitempty"`
		} `json:"tools,omitempty"`
	}

	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, fmt.Errorf("failed to parse Claude request: %w", err)
	}

	unified := &UnifiedRequest{
		Model:     claudeReq.Model,
		MaxTokens: claudeReq.MaxTokens,
		Stream:    claudeReq.Stream,
		Stop:      claudeReq.Stop,
	}

	// 正确处理指针类型
	if claudeReq.Temperature > 0 {
		unified.Temperature = &claudeReq.Temperature
	}
	if claudeReq.TopP > 0 {
		unified.TopP = &claudeReq.TopP
	}

	// 转换消息
	for _, msg := range claudeReq.Messages {
		unified.Messages = append(unified.Messages, UnifiedMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// 如果有 system 消息，添加到开头
	if claudeReq.System != "" {
		systemMsg := UnifiedMessage{
			Role:    "system",
			Content: claudeReq.System,
		}
		unified.Messages = append([]UnifiedMessage{systemMsg}, unified.Messages...)
	}

	// 转换思考配置
	if claudeReq.ThinkingEnabled != nil && *claudeReq.ThinkingEnabled {
		effort := "medium"
		if claudeReq.ThinkingBudget > 0 {
			if claudeReq.ThinkingBudget <= 1000 {
				effort = "low"
			} else if claudeReq.ThinkingBudget >= 20000 {
				effort = "high"
			}
		}
		unified.ThinkingConfig = &ThinkingConfig{
			Enabled: true,
			Effort:  effort,
		}
	}

	// 转换工具
	for _, tool := range claudeReq.Tools {
		unified.Tools = append(unified.Tools, Tool{
			Type: "function",
			Function: FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	return unified, nil
}

// Types for Gemini
type GeminiContent struct {
	Role  string        `json:"role"`
	Parts []GeminiPart  `json:"parts"`
}

type GeminiPart struct {
	Text            string              `json:"text,omitempty"`
	ExecutableCode   *GeminiExecutableCode `json:"executableCode,omitempty"`
	FunctionCall     interface{}         `json:"functionCall,omitempty"`
	FunctionResponse interface{}         `json:"functionResponse,omitempty"`
}

type GeminiExecutableCode struct {
	Language string `json:"language,omitempty"`
	Code     string `json:"code,omitempty"`
}

// ConvertFromUnified 从统一格式转换为目标平台格式
func ConvertFromUnified(unified *UnifiedRequest, targetPlatform Platform) ([]byte, error) {
	switch targetPlatform {
	case PlatformDeepSeek:
		return UnifiedToDeepSeek(unified)
	case PlatformOpenAI:
		return UnifiedToOpenAI(unified)
	case PlatformAnthropic:
		return UnifiedToClaude(unified)
	case PlatformGemini:
		return UnifiedToGemini(unified)
	default:
		return UnifiedToOpenAI(unified) // 默认转为 OpenAI 格式
	}
}

// UnifiedToOpenAI 将统一格式转换为 OpenAI 格式
func UnifiedToOpenAI(unified *UnifiedRequest) ([]byte, error) {
	result := make(map[string]interface{})

	result["model"] = unified.Model
	result["messages"] = unified.Messages

	if unified.MaxTokens > 0 {
		result["max_tokens"] = unified.MaxTokens
	}
	if unified.Temperature != nil {
		result["temperature"] = *unified.Temperature
	}
	if unified.TopP != nil {
		result["top_p"] = *unified.TopP
	}
	if unified.Stream {
		result["stream"] = unified.Stream
	}
	if unified.Stop != nil {
		result["stop"] = unified.Stop
	}
	if unified.PresencePenalty != nil {
		result["presence_penalty"] = *unified.PresencePenalty
	}
	if unified.FrequencyPenalty != nil {
		result["frequency_penalty"] = *unified.FrequencyPenalty
	}
	if len(unified.Tools) > 0 {
		result["tools"] = unified.Tools
	}
	if unified.ToolChoice != nil {
		result["tool_choice"] = unified.ToolChoice
	}

	// 处理思考配置 (OpenAI reasoning_effort)
	if unified.ThinkingConfig != nil && unified.ThinkingConfig.Enabled {
		result["reasoning_effort"] = unified.ThinkingConfig.Effort
	}

	return json.Marshal(result)
}

// UnifiedToDeepSeek 将统一格式转换为 DeepSeek 格式
// DeepSeek 基本兼容 OpenAI，但有一些限制
func UnifiedToDeepSeek(unified *UnifiedRequest) ([]byte, error) {
	result := make(map[string]interface{})

	result["model"] = unified.Model

	// DeepSeek 的 content 必须是字符串，不能是数组
	messages := make([]map[string]interface{}, len(unified.Messages))
	for i, msg := range unified.Messages {
		messages[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": normalizeContentForDeepSeek(msg.Content),
		}
	}
	result["messages"] = messages

	if unified.MaxTokens > 0 {
		result["max_tokens"] = unified.MaxTokens
	}
	if unified.Temperature != nil {
		result["temperature"] = *unified.Temperature
	}
	if unified.TopP != nil {
		result["top_p"] = *unified.TopP
	}
	if unified.Stream {
		result["stream"] = unified.Stream
	}
	if unified.Stop != nil {
		result["stop"] = unified.Stop
	}
	if unified.PresencePenalty != nil {
		result["presence_penalty"] = *unified.PresencePenalty
	}
	if unified.FrequencyPenalty != nil {
		result["frequency_penalty"] = *unified.FrequencyPenalty
	}

	// DeepSeek 不支持流式选项和其他高级参数

	return json.Marshal(result)
}

// UnifiedToClaude 将统一格式转换为 Claude 格式
func UnifiedToClaude(unified *UnifiedRequest) ([]byte, error) {
	result := make(map[string]interface{})

	result["model"] = unified.Model
	result["messages"] = unified.Messages
	if unified.MaxTokens > 0 {
		result["max_tokens"] = unified.MaxTokens
	}

	if unified.Temperature != nil {
		result["temperature"] = *unified.Temperature
	}
	if unified.TopP != nil {
		result["top_p"] = *unified.TopP
	}
	if unified.Stream {
		result["stream"] = unified.Stream
	}
	if unified.Stop != nil {
		result["stop"] = unified.Stop
	}

	// Claude 思考模式
	if unified.ThinkingConfig != nil && unified.ThinkingConfig.Enabled {
		enabled := true
		result["thinking_enabled"] = &enabled
		budget := 10000
		if unified.ThinkingConfig.Effort == "low" {
			budget = 1000
		} else if unified.ThinkingConfig.Effort == "high" {
			budget = 20000
		}
		result["thinking_budget"] = budget
	}

	return json.Marshal(result)
}

// UnifiedToGemini 将统一格式转换为 Gemini 格式
func UnifiedToGemini(unified *UnifiedRequest) ([]byte, error) {
	// Gemini API 格式结构
	type GeminiPart struct {
		Text string `json:"text,omitempty"`
	}

	type GeminiContent struct {
		Role  string       `json:"role"`
		Parts []GeminiPart `json:"parts"`
	}

	type GeminiRequest struct {
		Contents          []GeminiContent `json:"contents"`
		GenerationConfig  *struct {
			Temperature float64 `json:"temperature,omitempty"`
			MaxTokens   int     `json:"maxOutputTokens,omitempty"`
			TopP        float64 `json:"topP,omitempty"`
			TopK        int     `json:"topK,omitempty"`
		} `json:"generationConfig,omitempty"`
	}

	req := GeminiRequest{
		Contents: make([]GeminiContent, 0, len(unified.Messages)),
	}

	// 如果有参数，创建 generationConfig
	hasConfig := unified.Temperature != nil || unified.MaxTokens > 0 ||
		unified.TopP != nil || unified.TopK > 0

	if hasConfig {
		req.GenerationConfig = &struct {
			Temperature float64 `json:"temperature,omitempty"`
			MaxTokens   int     `json:"maxOutputTokens,omitempty"`
			TopP        float64 `json:"topP,omitempty"`
			TopK        int     `json:"topK,omitempty"`
		}{}

		if unified.Temperature != nil {
			req.GenerationConfig.Temperature = *unified.Temperature
		}
		if unified.MaxTokens > 0 {
			req.GenerationConfig.MaxTokens = unified.MaxTokens
		}
		if unified.TopP != nil {
			req.GenerationConfig.TopP = *unified.TopP
		}
		if unified.TopK > 0 {
			req.GenerationConfig.TopK = unified.TopK
		}
	}

	// 转换消息
	for _, msg := range unified.Messages {
		// Role 映射: assistant -> model
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		content := GeminiContent{
			Role: role,
		}

		// 处理 content (可能是字符串或数组)
		text := ""
		if msg.Content == nil {
			text = ""
		} else if str, ok := msg.Content.(string); ok {
			text = str
		} else {
			// 数组类型提取文本
			text = extractTextFromContent(msg.Content)
		}

		content.Parts = []GeminiPart{{Text: text}}
		req.Contents = append(req.Contents, content)
	}

	return json.Marshal(req)
}

// extractTextFromContent 从 content 中提取文本（支持多种格式）
func extractTextFromContent(content interface{}) string {
	if content == nil {
		return ""
	}

	// 字符串直接返回
	if str, ok := content.(string); ok {
		return str
	}

	// 数组类型提取文本
	if arr, ok := content.([]interface{}); ok {
		var textBuilder strings.Builder
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemType, ok := itemMap["type"].(string); ok {
					if itemType == "text" {
						if text, ok := itemMap["text"].(string); ok {
							textBuilder.WriteString(text)
						}
					}
				}
			}
		}
		return textBuilder.String()
	}

	// 其他情况，尝试转为字符串
	return fmt.Sprintf("%v", content)
}

// normalizeContentForDeepSeek 将 content 转换为 DeepSeek 支持的格式
func normalizeContentForDeepSeek(content interface{}) string {
	if content == nil {
		return ""
	}

	// 如果是字符串，直接返回
	if str, ok := content.(string); ok {
		return str
	}

	// 如果是数组，提取所有文本内容
	if arr, ok := content.([]interface{}); ok {
		var textBuilder strings.Builder
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemType, ok := itemMap["type"].(string); ok {
					if itemType == "text" {
						if text, ok := itemMap["text"].(string); ok {
							textBuilder.WriteString(text)
						}
					}
				}
			}
		}
		return textBuilder.String()
	}

	// 其他情况，尝试转为字符串
	return fmt.Sprintf("%v", content)
}

// MarshalUnifiedRequest 序列化统一请求为 JSON（用于日志）
func MarshalUnifiedRequest(req *UnifiedRequest) ([]byte, error) {
	return json.MarshalIndent(req, "", "  ")
}

// MarshalResponse 序列化响应为 JSON（用于日志）
func MarshalResponse(resp *OpenAIResponse) ([]byte, error) {
	return json.MarshalIndent(resp, "", "  ")
}
