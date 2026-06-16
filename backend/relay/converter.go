package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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

// APIFormat 是按「线路 API 协议」对模型源/模型的分类，取代旧的含糊 platform
// （把厂商和协议混为一谈）。四个值一一对应一种上游 wire API：
//   - APIFormatResponses        OpenAI Responses API（codex 用，选它即透传，不转换）
//   - APIFormatChatCompletions  OpenAI Chat Completions API（最通用的兼容协议）
//   - APIFormatAnthropic        Anthropic Messages API（/v1/messages）
//   - APIFormatGemini           Gemini API（/v1beta generateContent）
const (
	APIFormatResponses       = "responses"
	APIFormatChatCompletions = "chat_completions"
	APIFormatAnthropic       = "anthropic"
	APIFormatGemini          = "gemini"
)

// NormalizeAPIFormat 把任意历史 platform 值归一化到上述四个 apiFormat 之一。
// 用于读取时在线兼容旧库（无需数据迁移）：
//   - openai / openai-compatible / azure / deepseek / ""  → chat_completions
//   - claude / anthropic                                  → anthropic
//   - gemini / google                                     → gemini
//   - responses / openai_responses                        → responses
//
// 注意：旧的 "openai" 归一到 chat_completions（而非 responses），因为旧实现走的就是
// chat completions 转换路径；只有用户在新 UI 明确选 "responses" 才触发透传。
func NormalizeAPIFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case APIFormatResponses, "openai_responses", "openai-responses":
		return APIFormatResponses
	case APIFormatAnthropic, "claude":
		return APIFormatAnthropic
	case APIFormatGemini, "google":
		return APIFormatGemini
	default:
		// chat_completions / openai / openai-compatible / azure / deepseek / 未知
		return APIFormatChatCompletions
	}
}

// DetectPlatform 从 baseURL 或 platform 字段检测平台类型
func DetectPlatform(baseURL, platform string) Platform {
	// 首先检查明确的 platform / apiFormat 字段。
	// 同时识别新的 apiFormat 值（responses/chat_completions）与旧值（openai 等）。
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "openai", "chat_completions", "responses", "openai_responses", "openai-compatible":
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
	FormatOpenAI   FormatType = "openai"
	FormatDeepSeek FormatType = "deepseek"
	FormatGemini   FormatType = "gemini"
	FormatClaude   FormatType = "claude"
	FormatUnknown  FormatType = "unknown"
)

// FormatMatchesPlatform 判断客户端输入格式是否与所选上游线路 API 同源。
// 同源时可走零转换透传（PassthroughBody），无需经 unified 中间模型有损往返：
//
//	FormatClaude            ↔ PlatformAnthropic
//	FormatGemini            ↔ PlatformGemini
//	FormatOpenAI/FormatDeepSeek ↔ PlatformOpenAI/PlatformDeepSeek/PlatformAzure
//
// 注意：这里只看 wire 协议族是否一致，不区分具体厂商（OpenAI/DeepSeek/Azure 同属
// Chat Completions 协议族，请求/响应结构兼容，可互相透传）。
func FormatMatchesPlatform(inputFormat FormatType, platform Platform) bool {
	switch inputFormat {
	case FormatClaude:
		return platform == PlatformAnthropic
	case FormatGemini:
		return platform == PlatformGemini
	case FormatOpenAI, FormatDeepSeek:
		return platform == PlatformOpenAI || platform == PlatformDeepSeek || platform == PlatformAzure
	default:
		return false
	}
}

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
	Model               string           `json:"model"`
	Messages            []UnifiedMessage `json:"messages"`
	MaxTokens           int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Temperature         *float64         `json:"temperature,omitempty"`
	TopP                *float64         `json:"top_p,omitempty"`
	TopK                int              `json:"top_k,omitempty"`
	N                   int              `json:"n,omitempty"`
	Stream              bool             `json:"stream,omitempty"`
	StreamOptions       *StreamOptions   `json:"stream_options,omitempty"`
	Stop                interface{}      `json:"stop,omitempty"`

	// 惩罚参数
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// 思考模式相关 (OpenAI/Claude/Gemini)
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ThinkingConfig  *ThinkingConfig `json:"thinking_config,omitempty"`

	// 工具调用
	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        interface{} `json:"tool_choice,omitempty"`
	ParallelToolCalls bool        `json:"parallel_tool_calls,omitempty"`

	// 响应格式
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// 其他常用字段
	User        string  `json:"user,omitempty"`
	Seed        float64 `json:"seed,omitempty"`
	LogProbs    bool    `json:"logprobs,omitempty"`
	TopLogProbs int     `json:"top_logprobs,omitempty"`

	// SiliconFlow / 其他提供商特定字段
	PromptCacheKey       string          `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`

	// 预留扩展字段（使用 json.RawMessage 保留原始 JSON）
	ExtraFields map[string]json.RawMessage `json:"-"`
}

type UnifiedMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ThinkingConfig struct {
	Enabled bool   `json:"enabled"`
	Effort  string `json:"effort,omitempty"` // "low" | "medium" | "high"
}

// ConvertToUnified 将任意格式的请求转换为统一格式。
// hint 为路径推断的格式，FormatUnknown 时回退到字段检测。
func ConvertToUnified(body []byte, hint FormatType) (*UnifiedRequest, error) {
	format := hint
	if format == FormatUnknown {
		format = DetectInputFormat(body)
	}

	switch format {
	case FormatGemini:
		return GeminiToUnified(body)
	case FormatClaude:
		return ClaudeToUnified(body)
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
		Model:  reqString(req, "model"),
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
								Name:        reqString(funcMap, "name"),
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
		Model            string          `json:"model"`
		Contents         []GeminiContent `json:"contents"`
		GenerationConfig struct {
			Temperature *float64 `json:"temperature,omitempty"`
			MaxTokens   int      `json:"maxOutputTokens,omitempty"`
			TopP        *float64 `json:"topP,omitempty"`
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

	// 正确处理指针类型：用 *float64 区分「未设置」与「显式设为 0」（temperature=0
	// 是确定性输出的合法值，不能当未设丢弃）。
	if geminiReq.GenerationConfig.Temperature != nil {
		unified.Temperature = geminiReq.GenerationConfig.Temperature
	}
	if geminiReq.GenerationConfig.TopP != nil {
		unified.TopP = geminiReq.GenerationConfig.TopP
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
				// 累积文本；不要清空 contentParts，否则同消息内先出现的
				// executableCode/functionCall 会被后续文本部件抹掉。
				textContent.WriteString(part.Text)
			}
			if part.ExecutableCode != nil {
				contentParts = append(contentParts, map[string]interface{}{
					"type": "code",
					"code": part.ExecutableCode.Code,
				})
			}
			// functionCall / functionResponse 此前完全未转换，整段工具往返丢失。
			// 以保真为先：原样保留其结构体，交由下游 ConvertFromUnified 渲染。
			if part.FunctionCall != nil {
				contentParts = append(contentParts, map[string]interface{}{
					"type":         "function_call",
					"functionCall": part.FunctionCall,
				})
			}
			if part.FunctionResponse != nil {
				contentParts = append(contentParts, map[string]interface{}{
					"type":             "function_response",
					"functionResponse": part.FunctionResponse,
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
			Role    string      `json:"role"`
			Content interface{} `json:"content"`
		} `json:"messages"`
		System          interface{} `json:"system,omitempty"`
		Temperature     *float64    `json:"temperature,omitempty"`
		TopP            *float64    `json:"top_p,omitempty"`
		Stream          bool        `json:"stream,omitempty"`
		Stop            interface{} `json:"stop,omitempty"`
		ThinkingEnabled *bool       `json:"thinking_enabled,omitempty"`
		ThinkingBudget  int         `json:"thinking_budget,omitempty"`
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

	// 正确处理指针类型：解码为 *float64 后判 nil，保留合法的 0 值
	// （temperature=0 表示确定性输出，是有效值，不能当未设丢弃）。
	if claudeReq.Temperature != nil {
		unified.Temperature = claudeReq.Temperature
	}
	if claudeReq.TopP != nil {
		unified.TopP = claudeReq.TopP
	}

	// 转换消息
	for _, msg := range claudeReq.Messages {
		unified.Messages = append(unified.Messages, UnifiedMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// 如果有 system 消息，添加到开头（兼容 string / content blocks）
	if systemText := extractClaudeSystemText(claudeReq.System); systemText != "" {
		systemMsg := UnifiedMessage{
			Role:    "system",
			Content: systemText,
		}
		unified.Messages = append([]UnifiedMessage{systemMsg}, unified.Messages...)
	}

	// 转换思考配置
	if claudeReq.ThinkingEnabled != nil && *claudeReq.ThinkingEnabled {
		effort := effortFromBudget(claudeReq.ThinkingBudget)
		if effort == "" {
			effort = "medium"
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
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             string                `json:"text,omitempty"`
	ExecutableCode   *GeminiExecutableCode `json:"executableCode,omitempty"`
	FunctionCall     interface{}           `json:"functionCall,omitempty"`
	FunctionResponse interface{}           `json:"functionResponse,omitempty"`
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

// convertUnifiedMessagesToOpenAI 将统一消息转换为 OpenAI messages，重点兼容 Claude tool_use/tool_result。
func convertUnifiedMessagesToOpenAI(messages []UnifiedMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		if blocks, ok := msg.Content.([]interface{}); ok {
			if converted := convertClaudeBlocksMessageToOpenAI(msg.Role, blocks); len(converted) > 0 {
				result = append(result, converted...)
				continue
			}
		}

		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	return result
}

func convertClaudeBlocksMessageToOpenAI(role string, blocks []interface{}) []map[string]interface{} {
	var textBuilder strings.Builder
	var toolCalls []map[string]interface{}
	var toolMessages []map[string]interface{}
	sawClaudeToolBlock := false

	for _, block := range blocks {
		blockMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := blockMap["type"].(string)
		switch blockType {
		case "text":
			if text, ok := blockMap["text"].(string); ok {
				textBuilder.WriteString(text)
			}
		case "tool_use":
			sawClaudeToolBlock = true
			id, _ := blockMap["id"].(string)
			name, _ := blockMap["name"].(string)
			args := "{}"
			if input := blockMap["input"]; input != nil {
				if b, err := json.Marshal(input); err == nil {
					args = string(b)
				}
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   id,
				"type": "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": args,
				},
			})
		case "tool_result":
			sawClaudeToolBlock = true
			toolUseID, _ := blockMap["tool_use_id"].(string)
			toolMessages = append(toolMessages, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolUseID,
				"content":      extractTextFromContent(blockMap["content"]),
			})
		}
	}

	if !sawClaudeToolBlock {
		return nil
	}

	converted := make([]map[string]interface{}, 0, 1+len(toolMessages))
	if len(toolCalls) > 0 {
		converted = append(converted, map[string]interface{}{
			"role":       "assistant",
			"content":    textBuilder.String(),
			"tool_calls": toolCalls,
		})
	} else if textBuilder.Len() > 0 {
		converted = append(converted, map[string]interface{}{
			"role":    role,
			"content": textBuilder.String(),
		})
	}
	converted = append(converted, toolMessages...)

	return converted
}

// UnifiedToOpenAI 将统一格式转换为 OpenAI 格式
func UnifiedToOpenAI(unified *UnifiedRequest) ([]byte, error) {
	result := make(map[string]interface{})

	result["model"] = unified.Model
	result["messages"] = convertUnifiedMessagesToOpenAI(unified.Messages)

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
	if unified.StreamOptions != nil {
		result["stream_options"] = unified.StreamOptions
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
			"content": extractTextFromContent(msg.Content),
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

	// 提取 system 消息到顶层，Claude 不接受 messages 中的 system role
	var systemText string
	var filteredMessages []UnifiedMessage
	for _, msg := range unified.Messages {
		if msg.Role == "system" {
			if s, ok := msg.Content.(string); ok {
				if systemText == "" {
					systemText = s
				} else {
					systemText += "\n" + s
				}
			}
		} else {
			filteredMessages = append(filteredMessages, msg)
		}
	}
	if systemText != "" {
		result["system"] = systemText
	}
	if filteredMessages != nil {
		result["messages"] = filteredMessages
	} else {
		result["messages"] = []UnifiedMessage{}
	}

	if unified.MaxTokens > 0 {
		result["max_tokens"] = unified.MaxTokens
	} else {
		result["max_tokens"] = 65536 // Claude 要求必须有 max_tokens，默认 64K
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
		result["stop_sequences"] = unified.Stop
	}

	// Claude 思考模式：使用真实的 thinking 对象格式
	// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking
	if unified.ThinkingConfig != nil && unified.ThinkingConfig.Enabled {
		budget := budgetFromEffort(unified.ThinkingConfig.Effort)
		result["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": budget,
		}
		// 启用 thinking 时必须 temperature=1, 不能设置 top_p
		result["temperature"] = 1.0
		delete(result, "top_p")
	}

	// Claude 工具转换：OpenAI function.parameters → Claude input_schema
	if len(unified.Tools) > 0 {
		claudeTools := make([]map[string]interface{}, 0, len(unified.Tools))
		for _, tool := range unified.Tools {
			claudeTools = append(claudeTools, map[string]interface{}{
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": tool.Function.Parameters,
			})
		}
		result["tools"] = claudeTools
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
		Contents         []GeminiContent `json:"contents"`
		GenerationConfig *struct {
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

func extractClaudeSystemText(system interface{}) string {
	if system == nil {
		return ""
	}

	// Claude 传统格式：system 为字符串
	if s, ok := system.(string); ok {
		return s
	}

	// Claude 新格式：system 为 content block 数组
	// 复用通用提取逻辑，支持 [{type:"text", text:"..."}]
	if text := extractTextFromContent(system); text != "" {
		return text
	}

	// 兜底
	return fmt.Sprintf("%v", system)
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

// MarshalUnifiedRequest 序列化统一请求为 JSON（用于日志）
func MarshalUnifiedRequest(req *UnifiedRequest) ([]byte, error) {
	return json.MarshalIndent(req, "", "  ")
}

// MarshalResponse 序列化响应为 JSON（用于日志）
func MarshalResponse(resp *OpenAIResponse) ([]byte, error) {
	return json.MarshalIndent(resp, "", "  ")
}

// ConvertClaudeStreamToOpenAI 将 Claude SSE 流转换为 OpenAI SSE 格式写入 writer
func ConvertClaudeStreamToOpenAI(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	toolCallIndex := -1
	// 累积 usage：Claude 在 message_start.message.usage 带 input_tokens，
	// 在 message_delta.usage 带 output_tokens。OpenAI 流需要在结尾补一个带 usage
	// 的 chunk，否则下游按 OpenAI 协议计费会得到 0 token。
	inputTokens := 0
	outputTokens := 0
	usageSeen := false

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		var chunk map[string]interface{}

		switch eventType {
		case "message_start":
			if msg, ok := event["message"].(map[string]interface{}); ok {
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					if v, ok := numberValue(usage["input_tokens"]); ok {
						inputTokens = int(v)
						usageSeen = true
					}
					if v, ok := numberValue(usage["output_tokens"]); ok {
						outputTokens = int(v)
						usageSeen = true
					}
				}
			}
			chunk = map[string]interface{}{
				"object":  "chat.completion.chunk",
				"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"role": "assistant", "content": ""}, "finish_reason": nil}},
			}

		case "content_block_start":
			cbRaw, _ := event["content_block"].(map[string]interface{})
			if cbRaw == nil {
				continue
			}
			cbType, _ := cbRaw["type"].(string)
			if cbType == "tool_use" {
				toolCallIndex++
				toolID, _ := cbRaw["id"].(string)
				toolName, _ := cbRaw["name"].(string)
				chunk = map[string]interface{}{
					"object": "chat.completion.chunk",
					"choices": []map[string]interface{}{{
						"index": 0,
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{{
								"index": toolCallIndex,
								"id":    toolID,
								"type":  "function",
								"function": map[string]interface{}{
									"name":      toolName,
									"arguments": "",
								},
							}},
						},
						"finish_reason": nil,
					}},
				}
			}

		case "content_block_delta":
			deltaRaw, _ := event["delta"].(map[string]interface{})
			if deltaRaw == nil {
				continue
			}
			deltaType, _ := deltaRaw["type"].(string)
			switch deltaType {
			case "text_delta":
				text, _ := deltaRaw["text"].(string)
				chunk = map[string]interface{}{
					"object":  "chat.completion.chunk",
					"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"content": text}, "finish_reason": nil}},
				}
			case "thinking_delta":
				thinkingText, _ := deltaRaw["thinking"].(string)
				chunk = map[string]interface{}{
					"object":  "chat.completion.chunk",
					"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"reasoning_content": thinkingText}, "finish_reason": nil}},
				}
			case "input_json_delta":
				partialJSON, _ := deltaRaw["partial_json"].(string)
				idx := toolCallIndex
				if idx < 0 {
					idx = 0
				}
				chunk = map[string]interface{}{
					"object": "chat.completion.chunk",
					"choices": []map[string]interface{}{{
						"index": 0,
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{{
								"index":    idx,
								"function": map[string]interface{}{"arguments": partialJSON},
							}},
						},
						"finish_reason": nil,
					}},
				}
			}

		case "message_delta":
			deltaRaw, _ := event["delta"].(map[string]interface{})
			stopReason, _ := deltaRaw["stop_reason"].(string)
			finishReason := claudeStopReasonToOpenAI(stopReason)
			// Claude 在 message_delta.usage 带最终 output_tokens。
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				if v, ok := numberValue(usage["output_tokens"]); ok {
					outputTokens = int(v)
					usageSeen = true
				}
				if v, ok := numberValue(usage["input_tokens"]); ok {
					inputTokens = int(v)
					usageSeen = true
				}
			}
			chunk = map[string]interface{}{
				"object":  "chat.completion.chunk",
				"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{}, "finish_reason": finishReason}},
			}

		case "message_stop":
			// 在 [DONE] 之前补发一个带 usage 的空 choices chunk（对齐 OpenAI
			// stream_options.include_usage 语义），让下游能拿到 token 计费数据。
			if usageSeen {
				usageChunk := map[string]interface{}{
					"object":  "chat.completion.chunk",
					"choices": []map[string]interface{}{},
					"usage": map[string]interface{}{
						"prompt_tokens":     inputTokens,
						"completion_tokens": outputTokens,
						"total_tokens":      inputTokens + outputTokens,
					},
				}
				if usageJSON, err := json.Marshal(usageChunk); err == nil {
					_, _ = writer.Write([]byte("data: " + string(usageJSON) + "\n\n"))
					_ = writer.Flush()
				}
			}
			_, _ = writer.Write([]byte("data: [DONE]\n\n"))
			_ = writer.Flush()
			return nil
		}

		if chunk != nil {
			chunkJSON, err := json.Marshal(chunk)
			if err == nil {
				_, _ = writer.Write([]byte("data: " + string(chunkJSON) + "\n\n"))
				_ = writer.Flush()
			}
		}
	}

	return scanner.Err()
}

// ConvertOpenAIStreamToClaudeStream 将 OpenAI SSE 流转换为 Claude SSE 格式写入 writer。
// 同时支持文本增量与 OpenAI tool_calls 增量到 Claude tool_use/input_json_delta 的转换。
func ConvertOpenAIStreamToClaudeStream(resp *http.Response, writer StreamResponseWriter, model string) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	headerSent := false
	outputTokens := 0
	nextBlockIndex := 0
	activeBlockIndex := -1
	textBlockIndex := -1
	toolBlockIndexByOpenAIIndex := map[int]int{}

	writeEvent := func(event, data string) {
		_, _ = writer.WriteString("event: " + event + "\ndata: " + data + "\n\n")
		_ = writer.Flush()
	}

	ensureMessageStart := func() {
		if headerSent {
			return
		}
		headerSent = true
		startMsg, _ := json.Marshal(map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id": msgID, "type": "message", "role": "assistant",
				"content": []interface{}{}, "model": model,
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		})
		writeEvent("message_start", string(startMsg))
		writeEvent("ping", `{"type":"ping"}`)
	}

	stopActiveBlock := func() {
		if activeBlockIndex < 0 {
			return
		}
		blockStop, _ := json.Marshal(map[string]interface{}{"type": "content_block_stop", "index": activeBlockIndex})
		writeEvent("content_block_stop", string(blockStop))
		activeBlockIndex = -1
	}

	ensureTextBlock := func() {
		ensureMessageStart()
		if textBlockIndex >= 0 {
			activeBlockIndex = textBlockIndex
			return
		}
		textBlockIndex = nextBlockIndex
		nextBlockIndex++
		activeBlockIndex = textBlockIndex
		blockStart, _ := json.Marshal(map[string]interface{}{
			"type": "content_block_start", "index": textBlockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		})
		writeEvent("content_block_start", string(blockStart))
	}

	startToolBlock := func(openAIIndex int, toolID string, toolName string) int {
		ensureMessageStart()
		stopActiveBlock()

		if toolID == "" {
			toolID = fmt.Sprintf("call_%d", openAIIndex)
		}

		claudeIndex := nextBlockIndex
		nextBlockIndex++
		toolBlockIndexByOpenAIIndex[openAIIndex] = claudeIndex
		activeBlockIndex = claudeIndex

		blockStart, _ := json.Marshal(map[string]interface{}{
			"type": "content_block_start", "index": claudeIndex,
			"content_block": map[string]interface{}{
				"type":  "tool_use",
				"id":    toolID,
				"name":  toolName,
				"input": map[string]interface{}{},
			},
		})
		writeEvent("content_block_start", string(blockStart))
		return claudeIndex
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
			break
		}
		if data == "" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		choices, _ := chunk["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]interface{})
		if choice == nil {
			continue
		}
		delta, _ := choice["delta"].(map[string]interface{})

		if delta != nil {
			if text, ok := delta["content"].(string); ok && text != "" {
				if activeBlockIndex >= 0 && activeBlockIndex != textBlockIndex {
					stopActiveBlock()
				}
				ensureTextBlock()
				outputTokens++
				deltaMsg, _ := json.Marshal(map[string]interface{}{
					"type": "content_block_delta", "index": textBlockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": text},
				})
				writeEvent("content_block_delta", string(deltaMsg))
			}

			if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tc := range toolCalls {
					tcMap, _ := tc.(map[string]interface{})
					if tcMap == nil {
						continue
					}

					openAIIndex := int(numberFromMap(tcMap, "index"))
					toolID, _ := tcMap["id"].(string)
					funcMap, _ := tcMap["function"].(map[string]interface{})
					toolName := ""
					arguments := ""
					if funcMap != nil {
						toolName, _ = funcMap["name"].(string)
						arguments, _ = funcMap["arguments"].(string)
					}

					claudeIndex, exists := toolBlockIndexByOpenAIIndex[openAIIndex]
					if !exists {
						claudeIndex = startToolBlock(openAIIndex, toolID, toolName)
					} else {
						activeBlockIndex = claudeIndex
					}

					if arguments != "" {
						deltaMsg, _ := json.Marshal(map[string]interface{}{
							"type": "content_block_delta", "index": claudeIndex,
							"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": arguments},
						})
						writeEvent("content_block_delta", string(deltaMsg))
					}
				}
			}
		}

		if finishReason, _ := choice["finish_reason"].(string); finishReason != "" {
			ensureMessageStart()
			stopActiveBlock()

			stopReason := openAIFinishReasonToClaude(finishReason)
			msgDelta, _ := json.Marshal(map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
				"usage": map[string]interface{}{"output_tokens": outputTokens},
			})
			writeEvent("message_delta", string(msgDelta))
			writeEvent("message_stop", `{"type":"message_stop"}`)
			return nil
		}
	}

	if headerSent {
		stopActiveBlock()
		msgDelta, _ := json.Marshal(map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]interface{}{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]interface{}{"output_tokens": outputTokens},
		})
		writeEvent("message_delta", string(msgDelta))
		writeEvent("message_stop", `{"type":"message_stop"}`)
	}

	return scanner.Err()
}

// ForwardStreamRaw 直接逐行转发 SSE（不做格式转换，用于 Claude→Claude 直通）
func ForwardStreamRaw(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		_, _ = writer.WriteString(line + "\n")
		if line == "" {
			_ = writer.Flush()
		}
	}
	return scanner.Err()
}

// ConvertOpenAIStreamToGeminiStream 将 OpenAI SSE 流转换为 Gemini SSE 格式写入 writer
func ConvertOpenAIStreamToGeminiStream(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	emittedCandidate := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if usageRaw, ok := chunk["usage"].(map[string]interface{}); ok {
			u := map[string]interface{}{
				"promptTokenCount":     int(numberFromMap(usageRaw, "prompt_tokens")),
				"candidatesTokenCount": int(numberFromMap(usageRaw, "completion_tokens")),
				"totalTokenCount":      int(numberFromMap(usageRaw, "total_tokens")),
			}
			usageChunk := map[string]interface{}{
				"usageMetadata": u,
			}
			if err := writeSSEData(writer, usageChunk); err != nil {
				return err
			}
		}

		choicesRaw, ok := chunk["choices"].([]interface{})
		if !ok || len(choicesRaw) == 0 {
			continue
		}
		choice, _ := choicesRaw[0].(map[string]interface{})
		if choice == nil {
			continue
		}

		if deltaRaw, ok := choice["delta"].(map[string]interface{}); ok {
			parts := make([]map[string]interface{}, 0, 3)

			if text, ok := deltaRaw["content"].(string); ok && text != "" {
				parts = append(parts, map[string]interface{}{"text": text})
			}

			if reasoning, ok := deltaRaw["reasoning_content"].(string); ok && reasoning != "" {
				parts = append(parts, map[string]interface{}{
					"text":    reasoning,
					"thought": true,
				})
			}

			if toolCalls, ok := deltaRaw["tool_calls"].([]interface{}); ok {
				for _, tc := range toolCalls {
					tcMap, _ := tc.(map[string]interface{})
					if tcMap == nil {
						continue
					}
					funcMap, _ := tcMap["function"].(map[string]interface{})
					if funcMap == nil {
						continue
					}

					name, _ := funcMap["name"].(string)
					argsRaw := funcMap["arguments"]
					if argsRaw == nil {
						argsRaw = map[string]interface{}{}
					}

					parsedArgs := argsRaw
					if argStr, ok := argsRaw.(string); ok {
						if argStr == "" {
							parsedArgs = map[string]interface{}{}
						} else {
							var parsedObj interface{}
							if err := json.Unmarshal([]byte(argStr), &parsedObj); err == nil {
								parsedArgs = parsedObj
							} else {
								parsedArgs = map[string]interface{}{
									"__raw_arguments": argStr,
								}
							}
						}
					}

					part := map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": name,
							"args": parsedArgs,
						},
					}
					parts = append(parts, part)
				}
			}

			if len(parts) > 0 {
				geminiChunk := map[string]interface{}{
					"candidates": []map[string]interface{}{
						{
							"content": map[string]interface{}{
								"role":  "model",
								"parts": parts,
							},
						},
					},
				}
				if err := writeSSEData(writer, geminiChunk); err != nil {
					return err
				}
				emittedCandidate = true
			}
		}

		if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
			endCandidate := map[string]interface{}{
				"finishReason": openAIFinishReasonToGemini(finishReason),
			}

			if !emittedCandidate {
				endCandidate["content"] = map[string]interface{}{
					"role": "model",
					"parts": []map[string]interface{}{
						{"text": ""},
					},
				}
			}

			endChunk := map[string]interface{}{
				"candidates": []map[string]interface{}{endCandidate},
			}
			if err := writeSSEData(writer, endChunk); err != nil {
				return err
			}
			return nil
		}
	}

	return scanner.Err()
}

// ConvertClaudeStreamToGeminiStream 将 Claude SSE 流转换为 Gemini SSE 格式写入 writer
func ConvertClaudeStreamToGeminiStream(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_delta":
			deltaRaw, _ := event["delta"].(map[string]interface{})
			if deltaRaw == nil {
				continue
			}
			deltaType, _ := deltaRaw["type"].(string)
			if deltaType != "text_delta" {
				continue
			}
			text, _ := deltaRaw["text"].(string)
			if text == "" {
				continue
			}

			geminiChunk := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"content": map[string]interface{}{
							"role": "model",
							"parts": []map[string]interface{}{
								{"text": text},
							},
						},
					},
				},
			}
			if err := writeSSEData(writer, geminiChunk); err != nil {
				return err
			}

		case "message_delta":
			deltaRaw, _ := event["delta"].(map[string]interface{})
			stopReason, _ := deltaRaw["stop_reason"].(string)
			finishReason := geminiFinishReasonFromClaudeStopReason(stopReason)

			endChunk := map[string]interface{}{
				"candidates": []map[string]interface{}{
					{
						"finishReason": finishReason,
					},
				},
			}
			if err := writeSSEData(writer, endChunk); err != nil {
				return err
			}
			return nil
		}
	}

	return scanner.Err()
}

func geminiFinishReasonFromClaudeStopReason(stopReason string) string {
	switch stopReason {
	case "max_tokens":
		return "MAX_TOKENS"
	default:
		return "STOP"
	}
}

func writeSSEData(writer StreamResponseWriter, payload interface{}) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
		return err
	}
	return writer.Flush()
}

func numberFromMap(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func geminiFinishReasonToClaudeStopReason(reason string) string {
	switch reason {
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// ConvertGeminiStreamToClaudeStream 将 Gemini SSE 流转换为 Claude SSE 格式写入 writer
func ConvertGeminiStreamToClaudeStream(resp *http.Response, writer StreamResponseWriter, model string) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	headerSent := false
	inputTokens := 0
	outputTokens := 0

	writeEvent := func(event, data string) {
		_, _ = writer.WriteString("event: " + event + "\ndata: " + data + "\n\n")
		_ = writer.Flush()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk GeminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.UsageMetadata.PromptTokenCount > 0 {
			inputTokens = chunk.UsageMetadata.PromptTokenCount
		}
		if chunk.UsageMetadata.CandidatesTokenCount > 0 {
			outputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}

		for _, cand := range chunk.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text == "" {
					continue
				}

				if !headerSent {
					headerSent = true
					startMsg, _ := json.Marshal(map[string]interface{}{
						"type": "message_start",
						"message": map[string]interface{}{
							"id": msgID, "type": "message", "role": "assistant",
							"content": []interface{}{}, "model": model,
							"stop_reason": nil, "stop_sequence": nil,
							"usage": map[string]interface{}{"input_tokens": inputTokens, "output_tokens": 0},
						},
					})
					writeEvent("message_start", string(startMsg))

					blockStart, _ := json.Marshal(map[string]interface{}{
						"type": "content_block_start", "index": 0,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					})
					writeEvent("content_block_start", string(blockStart))
				}

				deltaMsg, _ := json.Marshal(map[string]interface{}{
					"type": "content_block_delta", "index": 0,
					"delta": map[string]interface{}{"type": "text_delta", "text": part.Text},
				})
				writeEvent("content_block_delta", string(deltaMsg))
			}

			if cand.FinishReason != "" {
				if !headerSent {
					headerSent = true
					startMsg, _ := json.Marshal(map[string]interface{}{
						"type": "message_start",
						"message": map[string]interface{}{
							"id": msgID, "type": "message", "role": "assistant",
							"content": []interface{}{}, "model": model,
							"stop_reason": nil, "stop_sequence": nil,
							"usage": map[string]interface{}{"input_tokens": inputTokens, "output_tokens": 0},
						},
					})
					writeEvent("message_start", string(startMsg))

					blockStart, _ := json.Marshal(map[string]interface{}{
						"type": "content_block_start", "index": 0,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					})
					writeEvent("content_block_start", string(blockStart))
				}

				blockStop, _ := json.Marshal(map[string]interface{}{"type": "content_block_stop", "index": 0})
				writeEvent("content_block_stop", string(blockStop))

				msgDelta, _ := json.Marshal(map[string]interface{}{
					"type": "message_delta",
					"delta": map[string]interface{}{
						"stop_reason":   geminiFinishReasonToClaudeStopReason(cand.FinishReason),
						"stop_sequence": nil,
					},
					"usage": map[string]interface{}{"output_tokens": outputTokens},
				})
				writeEvent("message_delta", string(msgDelta))
				writeEvent("message_stop", `{"type":"message_stop"}`)
				return nil
			}
		}
	}

	if headerSent {
		blockStop, _ := json.Marshal(map[string]interface{}{"type": "content_block_stop", "index": 0})
		writeEvent("content_block_stop", string(blockStop))
		msgDelta, _ := json.Marshal(map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
			},
			"usage": map[string]interface{}{"output_tokens": outputTokens},
		})
		writeEvent("message_delta", string(msgDelta))
		writeEvent("message_stop", `{"type":"message_stop"}`)
	}

	return scanner.Err()
}

// ConvertGeminiStreamToOpenAI 将 Gemini SSE 流转换为 OpenAI SSE 格式写入 writer
func ConvertGeminiStreamToOpenAI(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)

	sentRole := false
	toolIndexByKey := map[string]int{}
	nextToolIndex := 0

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if usageRaw, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			usageChunk := map[string]interface{}{
				"object":  "chat.completion.chunk",
				"choices": []interface{}{},
				"usage": map[string]interface{}{
					"prompt_tokens":     int(numberFromMap(usageRaw, "promptTokenCount")),
					"completion_tokens": int(numberFromMap(usageRaw, "candidatesTokenCount")),
					"total_tokens":      int(numberFromMap(usageRaw, "totalTokenCount")),
				},
			}
			if err := writeSSEData(writer, usageChunk); err != nil {
				return err
			}
		}

		cands, _ := chunk["candidates"].([]interface{})
		for _, c := range cands {
			cand, _ := c.(map[string]interface{})
			if cand == nil {
				continue
			}

			if !sentRole {
				roleChunk := map[string]interface{}{
					"object": "chat.completion.chunk",
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]interface{}{"role": "assistant"},
						"finish_reason": nil,
					}},
				}
				if err := writeSSEData(writer, roleChunk); err != nil {
					return err
				}
				sentRole = true
			}

			contentRaw, _ := cand["content"].(map[string]interface{})
			partsRaw, _ := contentRaw["parts"].([]interface{})

			for _, p := range partsRaw {
				part, _ := p.(map[string]interface{})
				if part == nil {
					continue
				}

				if text, ok := part["text"].(string); ok && text != "" {
					delta := map[string]interface{}{}
					if thought, _ := part["thought"].(bool); thought {
						delta["reasoning_content"] = text
					} else {
						delta["content"] = text
					}

					out := map[string]interface{}{
						"object": "chat.completion.chunk",
						"choices": []map[string]interface{}{{
							"index":         0,
							"delta":         delta,
							"finish_reason": nil,
						}},
					}
					if err := writeSSEData(writer, out); err != nil {
						return err
					}
				}

				if fcRaw, ok := part["functionCall"].(map[string]interface{}); ok {
					name, _ := fcRaw["name"].(string)
					key := name
					if key == "" {
						key = fmt.Sprintf("tool_%d", nextToolIndex)
					}

					index, exists := toolIndexByKey[key]
					if !exists {
						index = nextToolIndex
						nextToolIndex++
						toolIndexByKey[key] = index
					}

					args := fcRaw["args"]
					argsJSON := "{}"
					if args != nil {
						if b, err := json.Marshal(args); err == nil {
							argsJSON = string(b)
						}
					}

					out := map[string]interface{}{
						"object": "chat.completion.chunk",
						"choices": []map[string]interface{}{{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": []map[string]interface{}{{
									"index": index,
									"id":    fmt.Sprintf("call_%d", index),
									"type":  "function",
									"function": map[string]interface{}{
										"name":      name,
										"arguments": argsJSON,
									},
								}},
							},
							"finish_reason": nil,
						}},
					}
					if err := writeSSEData(writer, out); err != nil {
						return err
					}
				}
			}

			if finishReason, ok := cand["finishReason"].(string); ok && finishReason != "" {
				out := map[string]interface{}{
					"object": "chat.completion.chunk",
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": geminiFinishReasonToOpenAI(finishReason),
					}},
				}
				if err := writeSSEData(writer, out); err != nil {
					return err
				}
			}
		}
	}

	_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	_ = writer.Flush()
	return scanner.Err()
}
