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
	// Citations：text block 的引用标注（web_search_result_location 等），
	// 原样往返，不做跨协议语义翻译。
	Citations json.RawMessage `json:"citations,omitempty"`

	// RawFields：块的完整原始对象（UnmarshalJSON 捕获）。server_tool_use /
	// web_search_tool_result 等服务端工具块没有类型化字段，往返必须整块
	// 原样搬运（MarshalJSON 时以原始对象为底、类型化字段覆盖）。
	RawFields map[string]any `json:"-"`
}

// UnmarshalJSON 在类型化解码之外捕获完整原始块。
func (c *ClaudeContent) UnmarshalJSON(data []byte) error {
	type alias ClaudeContent
	var typed alias
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	*c = ClaudeContent(typed)
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		c.RawFields = raw
	}
	return nil
}

// MarshalJSON 以 RawFields 为底、非空类型化字段覆盖其上：服务端工具块的
	// 载荷完整保留；常规块退化为普通结构体序列化。
func (c ClaudeContent) MarshalJSON() ([]byte, error) {
	type alias ClaudeContent
	encoded, err := json.Marshal(alias(c))
	if err != nil {
		return nil, err
	}
	if len(c.RawFields) == 0 {
		return encoded, nil
	}
	var typed map[string]any
	if err := json.Unmarshal(encoded, &typed); err != nil {
		return nil, err
	}
	merged := make(map[string]any, len(c.RawFields)+len(typed))
	for key, value := range c.RawFields {
		merged[key] = value
	}
	for key, value := range typed {
		if value != nil {
			merged[key] = value
		}
	}
	return json.Marshal(merged)
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


// ConvertOpenAIResponseToClaude 将 OpenAI 响应转换为 Claude 原生格式
