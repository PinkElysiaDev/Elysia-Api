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
	return strings.TrimRight(strings.TrimSpace(baseUrl), "/") + path
}

type OpenAIAdapter struct {
	client *http.Client
	// streamClient 专用于流式请求：不设 Timeout。Go 的 http.Client.Timeout 覆盖
	// 整个请求生命周期（含读取 body），会把正常传输中的 SSE 长连接在 N 秒后无差别
	// 掐断（下游表现为"连接刚转发就被切断"）。流式只靠 Transport 的连接级超时控制。
	streamClient *http.Client
}

func NewOpenAIAdapter(timeout time.Duration) *OpenAIAdapter {
	// 连接时 SSRF 校验的安全 transport（newSecureTransport），杜绝 DNS rebinding。
	client := &http.Client{Transport: newSecureTransport()}
	if timeout > 0 {
		client.Timeout = timeout
	}
	// 流式 client：永不设 Timeout（对照 new-api 默认 RelayTimeout=0）。
	streamClient := &http.Client{Transport: newSecureTransport()}
	return &OpenAIAdapter{client: client, streamClient: streamClient}
}

// buildHTTPRequest 构建带有标准认证头的 HTTP 请求
func buildHTTPRequest(method, url, apiKey string, body []byte, extraHeaders map[string]string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	return req, nil
}

// OpenAIRequest 兼容 OpenAI API 格式
type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`

	MaxTokens           int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`

	Temperature   float64        `json:"temperature,omitempty"`
	TopP          float64        `json:"top_p,omitempty"`
	N             int            `json:"n,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	Stop interface{} `json:"stop,omitempty"`

	PresencePenalty  float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty float64 `json:"frequency_penalty,omitempty"`

	Seed int64  `json:"seed,omitempty"`
	User string `json:"user,omitempty"`

	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice interface{} `json:"tool_choice,omitempty"`

	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	ParallelToolCalls bool `json:"parallel_tool_calls,omitempty"`

	Prediction *Prediction `json:"prediction,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type ToolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type ResponseFormat struct {
	Type       string                 `json:"type"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

type Prediction struct {
	Type              string             `json:"type"`
	ContentPrediction *ContentPrediction `json:"content,omitempty"`
}

type ContentPrediction struct {
	Type string `json:"type"`
}

type Message struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

type OpenAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (m *Message) NormalizeContent() {
	if m.Content == nil {
		return
	}
	if _, ok := m.Content.(string); ok {
		return
	}

	arr, ok := m.Content.([]interface{})
	if !ok {
		return
	}
	if len(arr) == 0 {
		m.Content = ""
		return
	}
	if len(arr) == 1 {
		if item, ok := arr[0].(map[string]interface{}); ok {
			if itemType, ok := item["type"].(string); ok && itemType == "text" {
				if text, ok := item["text"].(string); ok {
					m.Content = text
					return
				}
			}
		}
	}
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type OpenAIResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
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
	UsageSemantic        string `json:"usage_semantic,omitempty"`
	UsageSource          string `json:"usage_source,omitempty"`

	PromptTokensDetails     PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	InputTokensDetails      PromptTokensDetails     `json:"input_tokens_details,omitempty"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	InputTokens             int                     `json:"input_tokens,omitempty"`
	OutputTokens            int                     `json:"output_tokens,omitempty"`
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
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	TextTokens      int `json:"text_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
	ImageTokens     int `json:"image_tokens,omitempty"`
}

func (a *OpenAIAdapter) SendRequest(baseUrl, apiKey string, req OpenAIRequest) (*OpenAIResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := openAIEndpoint(baseUrl, "/chat/completions")
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s", string(respBody))
	}

	var openAIResp OpenAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, err
	}

	return &openAIResp, nil
}

// SendRequestRaw 发送原始 JSON 请求体
func (a *OpenAIAdapter) SendRequestRaw(baseUrl, apiKey string, body []byte) (*OpenAIResponse, error) {
	url := openAIEndpoint(baseUrl, "/chat/completions")
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s", string(respBody))
	}

	var openAIResp OpenAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, err
	}

	return &openAIResp, nil
}

// SendRequestRawWithBody 发送原始请求体并返回解析结果、原始响应体和上游 HTTP
// 状态码。状态码用于上层故障转移决策（区分可重试的 5xx/429 与不可重试的 4xx）。
// 非 200 时返回 err，但 statusCode 仍为真实上游状态码；连接层错误时 statusCode=0。
func (a *OpenAIAdapter) SendRequestRawWithBody(baseUrl, apiKey string, body []byte) (*OpenAIResponse, []byte, int, error) {
	url := openAIEndpoint(baseUrl, "/chat/completions")
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, nil)
	if err != nil {
		return nil, nil, 0, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, respBody, resp.StatusCode, fmt.Errorf("API error: %s", string(respBody))
	}

	var openAIResp OpenAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, respBody, resp.StatusCode, err
	}

	return &openAIResp, respBody, resp.StatusCode, nil
}

func (a *OpenAIAdapter) SendResponsesRawWithBody(baseUrl, apiKey string, body []byte) (*OpenAIResponsesResponse, []byte, int, error) {
	url := openAIEndpoint(baseUrl, "/responses")
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, nil)
	if err != nil {
		return nil, nil, 0, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, respBody, resp.StatusCode, fmt.Errorf("API error: %s", string(respBody))
	}

	var responsesResp OpenAIResponsesResponse
	if err := json.Unmarshal(respBody, &responsesResp); err != nil {
		return nil, respBody, resp.StatusCode, err
	}

	return &responsesResp, respBody, resp.StatusCode, nil
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

// SendRequestStream 发送流式请求并返回原始 HTTP 响应
func (a *OpenAIAdapter) SendRequestStream(baseUrl, apiKey string, body []byte) (*http.Response, error) {
	url := openAIEndpoint(baseUrl, "/chat/completions")
	extraHeaders := map[string]string{
		"Accept": "text/event-stream",
	}
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, extraHeaders)
	if err != nil {
		return nil, err
	}

	resp, err := a.streamClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s", string(respBody))
	}

	return resp, nil
}

func (a *OpenAIAdapter) SendResponsesStream(baseUrl, apiKey string, body []byte) (*http.Response, error) {
	url := openAIEndpoint(baseUrl, "/responses")
	extraHeaders := map[string]string{
		"Accept": "text/event-stream",
	}
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, extraHeaders)
	if err != nil {
		return nil, err
	}

	resp, err := a.streamClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s", string(respBody))
	}

	return resp, nil
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
	streamTimeout := 5 * time.Minute

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
