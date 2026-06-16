package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// openAIVersionSegmentRe 匹配 URL path 中的版本段（/v1、/v2、/v1beta 等）。
// 仅检查 path，不会把 https://v1.example.com 这类 host 里的 v1 误判为版本段。
var openAIVersionSegmentRe = regexp.MustCompile(`(?i)/v\d`)

// normalizeOpenAIBaseURL 规范化 OpenAI 系（Chat Completions / Responses）的 base URL。
//
// OpenAI 兼容供应商约定 base 自带版本段（如 https://api.openai.com/v1），后端再拼
// /chat/completions 或 /responses。但用户常只填裸 host（如 https://moyuu.cc），
// 导致端点拼成 https://moyuu.cc/responses（少了 /v1）→ 上游 404 秒断、下游疯狂重试。
//
// 容错规则：path 已含版本段（/v1、/v2、/v1beta…）则原样用；否则自动补 /v1。
// 只作用于 OpenAI adapter——Claude/Gemini adapter 自己补 /v1、/v1beta，不能在此规范化。
func normalizeOpenAIBaseURL(baseUrl string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	if trimmed == "" {
		return trimmed
	}
	// 只解析 path 判断版本段，避免 host 中的 v\d 误判。解析失败则回退到原始串
	// 的整体匹配（保守起见仍尽量不重复补 /v1）。
	if u, err := url.Parse(trimmed); err == nil {
		if openAIVersionSegmentRe.MatchString(u.Path) {
			return trimmed
		}
		return trimmed + "/v1"
	}
	if openAIVersionSegmentRe.MatchString(trimmed) {
		return trimmed
	}
	return trimmed + "/v1"
}

// openAIEndpoint 用（可能是裸 host 的）base URL 拼出完整的 OpenAI 系端点 URL，
// path 形如 "/chat/completions" 或 "/responses"。
func openAIEndpoint(baseUrl, path string) string {
	return normalizeOpenAIBaseURL(baseUrl) + path
}

// versionTailRe 匹配 base URL 末尾的版本段：/v1、/v1beta、/v2 等（不区分大小写）。
var versionTailRe = regexp.MustCompile(`(?i)/v\d+[a-z]*$`)

// stripTrailingVersionSegment 去掉 base URL 末尾多余的版本段（/v1、/v1beta 等）。
//
// 用于 Claude/Gemini adapter：它们各自拼接完整版本路径（/v1/messages、
// /v1beta/models/...），约定 base 是裸 host。但用户常照 OpenAI 习惯在 base 末尾
// 填 /v1，导致拼成 /v1/v1/messages、/v1/v1beta/... 而 404。此函数把末尾的版本段
// 剥掉，让 base 回到不含版本的前缀，再由 adapter 拼自己的版本路径。
//
// 只看 path 末段，不会把 https://v1.example.com 这类 host 里的 v1 误剥。
func stripTrailingVersionSegment(baseUrl string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	if trimmed == "" {
		return trimmed
	}
	// 反复剥离末尾版本段，兼容 /v1beta/v1 这类多重误填。
	for {
		stripped := strings.TrimRight(versionTailRe.ReplaceAllString(trimmed, ""), "/")
		if stripped == trimmed {
			break
		}
		trimmed = stripped
	}
	return trimmed
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

// ForwardStreamResponse 转发 SSE 流式响应
func ForwardStreamResponse(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data == "[DONE]" {
				_, _ = writer.Write([]byte("data: [DONE]\n\n"))
				break
			}
			_, _ = writer.Write([]byte("data: " + data + "\n\n"))
		}
		_ = writer.Flush()
	}

	return scanner.Err()
}

// ForwardOpenAIStream 直接转发 OpenAI SSE 流（不做格式转换）
func ForwardOpenAIStream(resp *http.Response, writer StreamResponseWriter) error {
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		_, _ = writer.WriteString(scanner.Text() + "\n\n")
		_ = writer.Flush()
	}
	return scanner.Err()
}
