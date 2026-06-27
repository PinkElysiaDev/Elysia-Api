package relay

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ========== Gemini Adapter ==========

// GeminiAdapter 用于向 Gemini 原生 API 发送请求
type GeminiAdapter struct {
	client *http.Client
	// streamClient 专用于流式请求：不设 Timeout，避免长连接被硬超时掐断。
	streamClient *http.Client
}

func NewGeminiAdapter(timeout time.Duration) *GeminiAdapter {
	client := &http.Client{Transport: newSecureTransport()}
	if timeout > 0 {
		client.Timeout = timeout
	}
	streamClient := &http.Client{Transport: newSecureTransport()}
	return &GeminiAdapter{client: client, streamClient: streamClient}
}

// SendRequest 向 Gemini generateContent 端点发送请求，返回原始 HTTP 响应
func (a *GeminiAdapter) SendRequest(baseUrl, apiKey, model string, body []byte, isStream bool) (*http.Response, error) {
	base := strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	var url string
	if isStream {
		url = fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", base, model)
	} else {
		url = fmt.Sprintf("%s/v1beta/models/%s:generateContent", base, model)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
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
func geminiFinishReasonToOpenAI(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION":
		// 安全策略/复述拦截属于「被内容过滤」，映射为 content_filter，
		// 让调用方能区分「正常结束」与「被拦截」。
		return "content_filter"
	case "OTHER":
		return "stop"
	default:
		return "stop"
	}
}

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
func ConvertGeminiResponseToOpenAI(geminiResp *GeminiResponse) *OpenAIResponse {
	var textContent strings.Builder
	finishReason := "stop"

	if len(geminiResp.Candidates) > 0 {
		cand := geminiResp.Candidates[0]
		finishReason = geminiFinishReasonToOpenAI(cand.FinishReason)
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				textContent.WriteString(part.Text)
			}
		}
	}

	return &OpenAIResponse{
		ID:      "gemini-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "",
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: textContent.String()},
				FinishReason: finishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		},
	}
}

// ConvertOpenAIResponseToGemini 将 OpenAI 响应转换为 Gemini 原生格式
func ConvertOpenAIResponseToGemini(oaiResp *OpenAIResponse) *GeminiResponse {
	text := ""
	finishReason := "STOP"

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		text = extractTextFromContent(choice.Message.Content)
		finishReason = openAIFinishReasonToGemini(choice.FinishReason)
	}

	return &GeminiResponse{
		Candidates: []GeminiCandidate{
			{
				Content: GeminiContent{
					Role:  "model",
					Parts: []GeminiPart{{Text: text}},
				},
				FinishReason: finishReason,
			},
		},
		UsageMetadata: GeminiUsageMeta{
			PromptTokenCount:     oaiResp.Usage.PromptTokens,
			CandidatesTokenCount: oaiResp.Usage.CompletionTokens,
			TotalTokenCount:      oaiResp.Usage.TotalTokens,
		},
	}
}
