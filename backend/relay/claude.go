package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ========== Claude Adapter ==========

// ClaudeAdapter 用于向 Claude 原生 API 发送请求
type ClaudeAdapter struct {
	client *dynamicTimeoutClient
	// streamClient 专用于流式请求：不设 Timeout，避免长连接被硬超时掐断。
	streamClient *http.Client
}

func NewClaudeAdapter(timeout time.Duration) *ClaudeAdapter {
	return &ClaudeAdapter{client: newDynamicTimeoutClient(timeout), streamClient: &http.Client{Transport: newSecureTransport()}}
}

// SetTimeout 运行时更新非流式请求超时（admin 面板改 httpTimeout 后即时生效）。
func (a *ClaudeAdapter) SetTimeout(d time.Duration) { a.client.SetTimeout(d) }

// SendRequest 向 Claude /v1/messages 发送请求，返回原始 HTTP 响应。
// ctx 传播客户端取消信号（断连即中止上游调用）。
func (a *ClaudeAdapter) SendRequest(ctx context.Context, baseUrl, apiKey string, body []byte, isStream bool) (*http.Response, error) {
	url := strings.TrimRight(strings.TrimSpace(baseUrl), "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if isStream {
		req.Header.Set("Accept", "text/event-stream")
		return a.streamClient.Do(req)
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
	Type      string          `json:"type"` // "text" | "thinking" | "tool_use"
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`   // tool_use id
	Name      string          `json:"name,omitempty"` // tool_use name
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
	Source    map[string]any  `json:"source,omitempty"`
}

type ClaudeUsage struct {
	InputTokens              int                       `json:"input_tokens"`
	OutputTokens             int                       `json:"output_tokens"`
	CacheCreationInputTokens int                       `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                       `json:"cache_read_input_tokens,omitempty"`
	CacheCreation            *ClaudeCacheCreationUsage `json:"cache_creation,omitempty"`
	ServerToolUse            *ClaudeServerToolUse      `json:"server_tool_use,omitempty"`
}

type ClaudeCacheCreationUsage struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
}

type ClaudeServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests,omitempty"`
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
	case "refusal":
		// Claude 的 refusal（安全/政策拒答）映射为 content_filter，
		// 让调用方能区分「正常结束」与「被拒答」。
		return "content_filter"
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
	if len(toolCalls) > 0 {
		for _, toolCall := range toolCalls {
			function, _ := toolCall["function"].(map[string]interface{})
			message.ToolCalls = append(message.ToolCalls, OpenAIToolCall{
				ID:   stringFromMap(toolCall, "id"),
				Type: stringFromMap(toolCall, "type"),
				Function: OpenAIToolFunction{
					Name:      stringFromMap(function, "name"),
					Arguments: stringFromMap(function, "arguments"),
				},
			})
		}
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

func stringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
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

		if len(choice.Message.ToolCalls) > 0 {
			stopReason = "tool_use"
			for _, toolCall := range choice.Message.ToolCalls {
				input := json.RawMessage([]byte("{}"))
				if strings.TrimSpace(toolCall.Function.Arguments) != "" {
					input = json.RawMessage(toolCall.Function.Arguments)
				}
				content = append(content, ClaudeContent{
					Type:  "tool_use",
					ID:    toolCall.ID,
					Name:  toolCall.Function.Name,
					Input: input,
				})
			}
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
