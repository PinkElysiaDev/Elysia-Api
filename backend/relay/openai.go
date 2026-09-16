package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openAIEndpoint 用用户配置的 base URL 直接拼接端点 path。
// 用户负责配置正确的 base URL（如 https://api.openai.com/v1），后端不做任何规范化。
func openAIEndpoint(baseUrl, path string) string {
	return joinBasePath(baseUrl, path)
}

type OpenAIAdapter struct {
	client *dynamicTimeoutClient
	// streamClient 专用于流式请求：不设 Timeout。Go 的 http.Client.Timeout 覆盖
	// 整个请求生命周期（含读取 body），会把正常传输中的 SSE 长连接在 N 秒后无差别
	// 掐断（下游表现为"连接刚转发就被切断"）。流式只靠 Transport 的连接级超时控制。
	streamClient *http.Client
}

func NewOpenAIAdapter(timeout time.Duration) *OpenAIAdapter {
	return &OpenAIAdapter{client: newDynamicTimeoutClient(timeout), streamClient: &http.Client{Transport: newSecureTransport()}}
}

// SetTimeout 运行时更新非流式请求超时（admin 面板改 httpTimeout 后即时生效）。
func (a *OpenAIAdapter) SetTimeout(d time.Duration) { a.client.SetTimeout(d) }

// buildHTTPRequest 构建带有标准认证头的 HTTP 请求。ctx 传播客户端请求的
// 取消信号：客户端断连后上游调用随之中止，不再白耗带宽与上游配额。
func buildHTTPRequest(ctx context.Context, method, url, apiKey string, body []byte, extraHeaders map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	return req, nil
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Message struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	// ReasoningDetails 是 OpenRouter 风格的推理明细数组（reasoning.text /
	// reasoning.encrypted 逐条成项），往返保真优于标量 reasoning_content。
	ReasoningDetails []map[string]any `json:"reasoning_details,omitempty"`
	Refusal          string           `json:"refusal,omitempty"`
	Audio            interface{}      `json:"audio,omitempty"`
	Name             string           `json:"name,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type OpenAIToolCall struct {
	ID           string                      `json:"id"`
	Type         string                      `json:"type"`
	Function     OpenAIToolFunction          `json:"function"`
	ExtraContent *OpenAIToolCallExtraContent `json:"extra_content,omitempty"`
}

type OpenAIToolCallExtraContent struct {
	Google *OpenAIToolCallGoogleExtraContent `json:"google,omitempty"`
}

type OpenAIToolCallGoogleExtraContent struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type OpenAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	// SystemFingerprint：上游的版本指纹（模型权重/配置版本标识），往返保真。
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens         int    `json:"prompt_tokens"`
	CompletionTokens     int    `json:"completion_tokens"`
	TotalTokens          int    `json:"total_tokens"`
	CachedTokens         int    `json:"cached_tokens,omitempty"`
	PromptCacheHitTokens int    `json:"prompt_cache_hit_tokens,omitempty"`

	// details 用指针：值结构体的 omitempty 不生效（永远序列化成 {}），
	// 会覆盖 RawFields 透传的同键子对象。
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	InputTokensDetails      *PromptTokensDetails     `json:"input_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	InputTokens             int                      `json:"input_tokens,omitempty"`
	OutputTokens            int                      `json:"output_tokens,omitempty"`

	// RawFields：usage 的完整原始对象（UnmarshalJSON 捕获）。上游新增的
	// 计数键在跨协议中转时不丢失——MarshalJSON 以原始对象为底、类型化
	// 字段覆盖其上（XF5b：不重释、不丢弃）。
	RawFields map[string]any `json:"-"`
}

// UnmarshalJSON 在类型化解码之外捕获完整原始 usage 对象。
func (u *Usage) UnmarshalJSON(data []byte) error {
	type alias Usage
	var typed alias
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	*u = Usage(typed)
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		u.RawFields = raw
	}
	return nil
}

// MarshalJSON 以 RawFields 为底、非空类型化字段覆盖其上：上游新增计数键
// 原样透传；常规路径退化为普通结构体序列化。
func (u Usage) MarshalJSON() ([]byte, error) {
	type alias Usage
	return mergeRawOverTyped(u.RawFields, alias(u))
}

type PromptTokensDetails struct {
	CachedTokens         int `json:"cached_tokens,omitempty"`
	CacheReadTokens      int `json:"cache_read_tokens,omitempty"`
	CachedCreationTokens int `json:"cached_creation_tokens,omitempty"`
	TextTokens           int `json:"text_tokens,omitempty"`
	AudioTokens          int `json:"audio_tokens,omitempty"`
	ImageTokens          int `json:"image_tokens,omitempty"`
}

type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	TextTokens               int `json:"text_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	ImageTokens              int `json:"image_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// postJSONDecode POST JSON 请求体并解码为 T，附带原始响应体与上游状态码。
// 状态码用于上层故障转移决策（区分可重试的 5xx/429 与不可重试的 4xx）。
// 非 200 时返回 err，但 statusCode 仍为真实上游状态码；连接层错误时 statusCode=0。
func postJSONDecode[T any](a *OpenAIAdapter, ctx context.Context, url, apiKey string, body []byte) (*T, []byte, int, error) {
	httpReq, err := buildHTTPRequest(ctx, "POST", url, apiKey, body, nil)
	if err != nil {
		return nil, nil, 0, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamBodyBytes))
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, respBody, resp.StatusCode, fmt.Errorf("API error: %s", string(respBody))
	}

	var decoded T
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, respBody, resp.StatusCode, err
	}

	return &decoded, respBody, resp.StatusCode, nil
}

// SendRequestRawWithBody 发送 /chat/completions 请求并解码 OpenAIResponse。
func (a *OpenAIAdapter) SendRequestRawWithBody(ctx context.Context, baseUrl, apiKey string, body []byte) (*OpenAIResponse, []byte, int, error) {
	return postJSONDecode[OpenAIResponse](a, ctx, openAIEndpoint(baseUrl, "/chat/completions"), apiKey, body)
}

// SendResponsesRawWithBody 发送 /responses 请求并解码 OpenAIResponsesResponse。
func (a *OpenAIAdapter) SendResponsesRawWithBody(ctx context.Context, baseUrl, apiKey string, body []byte) (*OpenAIResponsesResponse, []byte, int, error) {
	return postJSONDecode[OpenAIResponsesResponse](a, ctx, openAIEndpoint(baseUrl, "/responses"), apiKey, body)
}

// IsStreamRequest 检查请求体是否为流式请求
func IsStreamRequest(body []byte) bool {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	if stream, ok := req["stream"].(bool); ok {
		return stream
	}
	return false
}

// postStream 发送 SSE 流式请求；非 200 时读尽错误体并以 UpstreamStatusError
// 携带真实状态码（供上层决定重试分类），成功时由调用方负责关闭 Body。
func (a *OpenAIAdapter) postStream(ctx context.Context, url, apiKey string, body []byte) (*http.Response, error) {
	extraHeaders := map[string]string{
		"Accept": "text/event-stream",
	}
	httpReq, err := buildHTTPRequest(ctx, "POST", url, apiKey, body, extraHeaders)
	if err != nil {
		return nil, err
	}

	resp, err := a.streamClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamBodyBytes))
		return nil, &UpstreamStatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	return resp, nil
}

// SendRequestStream 发送 /chat/completions 流式请求并返回原始 HTTP 响应。
func (a *OpenAIAdapter) SendRequestStream(ctx context.Context, baseUrl, apiKey string, body []byte) (*http.Response, error) {
	return a.postStream(ctx, openAIEndpoint(baseUrl, "/chat/completions"), apiKey, body)
}

// SendResponsesStream 发送 /responses 流式请求并返回原始 HTTP 响应。
func (a *OpenAIAdapter) SendResponsesStream(ctx context.Context, baseUrl, apiKey string, body []byte) (*http.Response, error) {
	return a.postStream(ctx, openAIEndpoint(baseUrl, "/responses"), apiKey, body)
}

// UpstreamStatusError 携带上游真实状态码：调用方据此决定对客户端的响应码
// 与重试分类——401/403/400 等永久错误不得洗白成 502 后被当作可重试错误
// 对全部候选扇出。
type UpstreamStatusError struct {
	StatusCode int
	Body       string
}

func (e *UpstreamStatusError) Error() string {
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Body)
}

// StreamResponseWriter 流式响应写入接口
type StreamResponseWriter interface {
	Write(data []byte) (int, error)
	WriteString(data string) (int, error)
	Flush() error
}

// ForwardOpenAIStream 直接转发 OpenAI SSE 流（不做格式转换）。
func ForwardOpenAIStream(ctx context.Context, resp *http.Response, writer StreamResponseWriter) error {
	return forwardSSELines(ctx, resp, writer, false)
}

// forwardSSELines 逐行转发 SSE 流，在空行处 flush。
// finalFlush=true 时在 EOF 后额外 flush 一次（Responses 协议需要此行为）。
// 使用 context 和超时保护避免长工作流中的连接静默断开。
func forwardSSELines(ctx context.Context, resp *http.Response, writer StreamResponseWriter, finalFlush bool) error {
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)
	// 5分钟流式读超时：Claude 思考模式可能长时间不吐 token，但正常不应超过 5 分钟无响应。
	// 每成功读取一行后重置超时，防止长工作流被 IdleConnTimeout 断开。
	streamTimeout := DefaultSSEIdleTimeout

	for {
		line, hasMore, err := scanSSEWithTimeout(ctx, scanner, streamTimeout)
		if err != nil {
			return err
		}
		if !hasMore {
			break
		}
		_, _ = writer.WriteString(line + "\n")
		if strings.TrimSpace(line) == "" {
			_ = writer.Flush()
		}
	}
	if finalFlush {
		_ = writer.Flush()
	}
	return nil
}
