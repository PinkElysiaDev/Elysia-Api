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

// APIFormat 是按「线路 API 协议」对模型源/模型的分类，取代旧的含糊 platform
// （把厂商和协议混为一谈）。四个值一一对应一种上游 wire API：
//   - APIFormatResponses        OpenAI Responses API（codex 用；默认仍经过 Maheshvara）
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
// chat completions 转换路径；只有显式开启 relay.passthrough 且协议同源时才允许透传。
func NormalizeAPIFormat(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(normalized, "custom:") && strings.TrimPrefix(normalized, "custom:") != "" {
		return normalized
	}
	switch normalized {
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
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(platform)), "custom:") {
		return Platform(strings.ToLower(strings.TrimSpace(platform)))
	}
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

func IsCustomPlatform(platform Platform) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(platform))), "custom:")
}

func CustomProtocolID(platform Platform) string {
	if !IsCustomPlatform(platform) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(string(platform))), "custom:"))
}

func TargetFormatForPlatform(platform Platform) (FormatType, error) {
	if IsCustomPlatform(platform) {
		return FormatUnknown, fmt.Errorf("custom platform %q requires a registered protocol renderer", platform)
	}
	switch platform {
	case PlatformAnthropic:
		return FormatClaude, nil
	case PlatformGemini:
		return FormatGemini, nil
	case PlatformOpenAI, PlatformDeepSeek, PlatformAzure, PlatformUnknown:
		return FormatOpenAIChat, nil
	default:
		return FormatUnknown, fmt.Errorf("unsupported target platform %q", platform)
	}
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
// Deprecated: use MaheshvaraRequest (CanonicalRequest).

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             string                `json:"text,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
	ThoughtSignature string                `json:"thoughtSignature,omitempty"`
	InlineData       map[string]any        `json:"inlineData,omitempty"`
	FileData         map[string]any        `json:"fileData,omitempty"`
	ExecutableCode   any                    `json:"executableCode,omitempty"`
	FunctionCall     interface{}           `json:"functionCall,omitempty"`
	FunctionResponse interface{}           `json:"functionResponse,omitempty"`
}

// extractTextFromContent 从 Claude system / Gemini parts 等内容结构中提取纯文本。
func extractTextFromContent(content interface{}) string {
	if content == nil {
		return ""
	}
	if str, ok := content.(string); ok {
		return str
	}
	if arr, ok := content.([]interface{}); ok {
		var textBuilder strings.Builder
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemMap["type"] == "text" {
					if text, ok := itemMap["text"].(string); ok {
						textBuilder.WriteString(text)
					}
				}
			}
		}
		return textBuilder.String()
	}
	return fmt.Sprintf("%v", content)
}
