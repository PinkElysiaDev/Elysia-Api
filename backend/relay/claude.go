package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ========== Claude Adapter ==========

// ClaudeAdapter 用于向 Claude 原生 API 发送请求
type ClaudeAdapter struct {
	client *http.Client
}

func NewClaudeAdapter(timeout time.Duration) *ClaudeAdapter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return &ClaudeAdapter{client: client}
}

// SendRequest 向 Claude /v1/messages 发送请求，返回原始 HTTP 响应
func (a *ClaudeAdapter) SendRequest(baseUrl, apiKey string, body []byte, isStream bool) (*http.Response, error) {
	url := strings.TrimSuffix(baseUrl, "/") + "/v1/messages"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if isStream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return a.client.Do(req)
}

// ClaudeResponse Claude 原生响应结构
type ClaudeResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Content    []ClaudeContent `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      ClaudeUsage     `json:"usage"`
}

type ClaudeContent struct {
	Type     string          `json:"type"` // "text" | "thinking" | "tool_use"
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`   // tool_use id
	Name     string          `json:"name,omitempty"` // tool_use name
	Input    json.RawMessage `json:"input,omitempty"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// claudeStopReasonToOpenAI 将 Claude stop_reason 映射为 OpenAI finish_reason
func claudeStopReasonToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}

// openAIFinishReasonToClaude 将 OpenAI finish_reason 映射为 Claude stop_reason
func openAIFinishReasonToClaude(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ConvertClaudeResponseToOpenAI 将 Claude 响应转换为 OpenAI 格式
func ConvertClaudeResponseToOpenAI(claudeResp *ClaudeResponse) *OpenAIResponse {
	var textContent strings.Builder
	var toolCalls []map[string]interface{}

	for _, block := range claudeResp.Content {
		switch block.Type {
		case "text":
			textContent.WriteString(block.Text)
		case "tool_use":
			toolCall := map[string]interface{}{
				"id":   block.ID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      block.Name,
					"arguments": string(block.Input),
				},
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	message := Message{
		Role:    "assistant",
		Content: textContent.String(),
	}

	finishReason := claudeStopReasonToOpenAI(claudeResp.StopReason)

	return &OpenAIResponse{
		ID:      claudeResp.ID,
		Object:  "chat.completion",
		Created: 0,
		Model:   claudeResp.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      message,
				FinishReason: finishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     claudeResp.Usage.InputTokens,
			CompletionTokens: claudeResp.Usage.OutputTokens,
			TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
		},
	}
}

// ConvertOpenAIResponseToClaude 将 OpenAI 响应转换为 Claude 原生格式
func ConvertOpenAIResponseToClaude(oaiResp *OpenAIResponse) *ClaudeResponse {
	var content []ClaudeContent
	stopReason := "end_turn"

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		stopReason = openAIFinishReasonToClaude(choice.FinishReason)

		if text, ok := choice.Message.Content.(string); ok && text != "" {
			content = append(content, ClaudeContent{Type: "text", Text: text})
		}
	}

	if len(content) == 0 {
		content = []ClaudeContent{{Type: "text", Text: ""}}
	}

	return &ClaudeResponse{
		ID:         oaiResp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      oaiResp.Model,
		StopReason: stopReason,
		Usage: ClaudeUsage{
			InputTokens:  oaiResp.Usage.PromptTokens,
			OutputTokens: oaiResp.Usage.CompletionTokens,
		},
	}
}
