package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ========== Gemini Adapter ==========

// GeminiAdapter 用于向 Gemini 原生 API 发送请求
type GeminiAdapter struct {
	client *dynamicTimeoutClient
	// streamClient 专用于流式请求：不设 Timeout，避免长连接被硬超时掐断。
	streamClient *http.Client
}

func NewGeminiAdapter(timeout time.Duration) *GeminiAdapter {
	return &GeminiAdapter{client: newDynamicTimeoutClient(timeout), streamClient: &http.Client{Transport: newSecureTransport()}}
}

// SetTimeout 运行时更新非流式请求超时（admin 面板改 httpTimeout 后即时生效）。
func (a *GeminiAdapter) SetTimeout(d time.Duration) { a.client.SetTimeout(d) }

// SendRequest 向 Gemini generateContent 端点发送请求，返回原始 HTTP 响应。
// ctx 传播客户端取消信号（断连即中止上游调用）。
func (a *GeminiAdapter) SendRequest(ctx context.Context, baseUrl, apiKey, model string, body []byte, isStream bool) (*http.Response, error) {
	base := strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	var url string
	if isStream {
		url = fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", base, model)
	} else {
		url = fmt.Sprintf("%s/v1beta/models/%s:generateContent", base, model)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	if isStream {
		req.Header.Set("Accept", "text/event-stream")
		return a.streamClient.Do(req)
	}
	return a.client.Do(req)
}

// GeminiResponse Gemini 原生响应结构
type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata GeminiUsageMeta   `json:"usageMetadata"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
	ResponseID    string            `json:"responseId,omitempty"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	// GroundingMetadata：搜索/据实生成的来源标注（groundingChunks/queries/
	// citations），原样保真往返，不做跨协议语义翻译。
	GroundingMetadata json.RawMessage `json:"groundingMetadata,omitempty"`
}

type GeminiUsageMeta struct {
	PromptTokenCount           int                 `json:"promptTokenCount"`
	ToolUsePromptTokenCount    int                 `json:"toolUsePromptTokenCount,omitempty"`
	CandidatesTokenCount       int                 `json:"candidatesTokenCount"`
	TotalTokenCount            int                 `json:"totalTokenCount"`
	ThoughtsTokenCount         int                 `json:"thoughtsTokenCount,omitempty"`
	CachedContentTokenCount    int                 `json:"cachedContentTokenCount,omitempty"`
	PromptTokensDetails        []GeminiTokenDetail `json:"promptTokensDetails,omitempty"`
	ToolUsePromptTokensDetails []GeminiTokenDetail `json:"toolUsePromptTokensDetails,omitempty"`
	CandidatesTokensDetails    []GeminiTokenDetail `json:"candidatesTokensDetails,omitempty"`
}

type GeminiTokenDetail struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// geminiFinishReasonToOpenAI 将 Gemini finishReason 映射为 OpenAI finish_reason

// openAIFinishReasonToGemini 将 OpenAI finish_reason 映射为 Gemini finishReason
func openAIFinishReasonToGemini(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "tool_calls":
		return "STOP"
	default:
		return "STOP"
	}
}

// ConvertGeminiResponseToOpenAI 将 Gemini 响应转换为 OpenAI 格式

// ConvertOpenAIResponseToGemini 将 OpenAI 响应转换为 Gemini 原生格式
