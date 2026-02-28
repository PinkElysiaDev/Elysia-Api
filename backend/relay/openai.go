package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIAdapter struct {
	client *http.Client
}

func NewOpenAIAdapter(timeout time.Duration) *OpenAIAdapter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	// timeout > 0 时才设置超时
	if timeout > 0 {
		client.Timeout = timeout
	}
	return &OpenAIAdapter{
		client: client,
	}
}

// buildHTTPRequest 构建带有标准认证头的 HTTP 请求
func buildHTTPRequest(method, url, apiKey string, body []byte, extraHeaders map[string]string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// 添加额外的头部
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	return req, nil
}

// OpenAIRequest 兼容 OpenAI API 格式
type OpenAIRequest struct {
	// 基础参数
	Model    string   `json:"model"`              // 必填
	Messages []Message `json:"messages"`           // 必填

	// 生成的tokens数量限制
	MaxTokens       int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`

	// 采样参数
	Temperature      float64 `json:"temperature,omitempty"`
	TopP             float64 `json:"top_p,omitempty"`
	N                int     `json:"n,omitempty"`              // 生成多少个choices
	Stream           bool    `json:"stream,omitempty"`         // 是否流式输出
	StreamOptions    *StreamOptions `json:"stream_options,omitempty"` // 流式选项

	// 停止条件
	Stop interface{} `json:"stop,omitempty"` // string 或 []string

	// 惩罚参数
	PresencePenalty  float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty float64 `json:"frequency_penalty,omitempty"`

	// 其他参数
	Seed             int64    `json:"seed,omitempty"`
	User             string   `json:"user,omitempty"`

	// 函数调用
	Tools            []Tool   `json:"tools,omitempty"`
	ToolChoice       interface{} `json:"tool_choice,omitempty"` // string 或 ToolChoice

	// 响应格式
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`

	// 并行调用
	ParallelToolCalls bool   `json:"parallel_tool_calls,omitempty"`

	// 预测输出
	Prediction *Prediction `json:"prediction,omitempty"`

	// 推理参数
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // "low" | "medium" | "high"
}

// StreamOptions 流式输出选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// Tool 工具定义
type Tool struct {
	Type     string                 `json:"type"` // "function"
	Function FunctionDefinition     `json:"function"`
}

type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolChoice 工具选择
type ToolChoice struct {
	Type     string `json:"type"`     // "function"
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// ResponseFormat 响应格式
type ResponseFormat struct {
	Type       string                 `json:"type"` // "text" | "json_object" | "json_schema"
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// Prediction 预测输出
type Prediction struct {
	Type string `json:"type"` // "content" | "summary"
	ContentPrediction *ContentPrediction `json:"content,omitempty"`
}

type ContentPrediction struct {
	Type string `json:"type"` // "content" | "content_summary"
}

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // 可以是 string 或 []ContentPart
}

// NormalizeContent 将 content 规范化为适合发送到 API 的格式
// 如果 content 是只包含单个 text 元素的数组，则转换为字符串
// 否则保持原样
func (m *Message) NormalizeContent() {
	if m.Content == nil {
		return
	}

	// 如果已经是字符串，无需处理
	if _, ok := m.Content.(string); ok {
		return
	}

	// 尝试处理数组格式
	arr, ok := m.Content.([]interface{})
	if !ok {
		return
	}

	// 如果数组为空，设置为空字符串
	if len(arr) == 0 {
		m.Content = ""
		return
	}

	// 检查是否只包含一个 text 类型的元素
	// 并且没有图片等其他类型的内容
	if len(arr) == 1 {
		if item, ok := arr[0].(map[string]interface{}); ok {
			if itemType, ok := item["type"].(string); ok && itemType == "text" {
				if text, ok := item["text"].(string); ok {
					m.Content = text // 转换为纯字符串
					return
				}
			}
		}
	}

	// 否则保持原样（多模态内容）
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
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
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (a *OpenAIAdapter) SendRequest(baseUrl, apiKey string, req OpenAIRequest) (*OpenAIResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseUrl, "/"))
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
	url := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseUrl, "/"))
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
// 调用方需要负责关闭 resp.Body
func (a *OpenAIAdapter) SendRequestStream(baseUrl, apiKey string, body []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseUrl, "/"))
	extraHeaders := map[string]string{
		"Accept": "text/event-stream",
	}
	httpReq, err := buildHTTPRequest("POST", url, apiKey, body, extraHeaders)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(httpReq)
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
		// SSE 格式：每行以 "data: " 开头
		if strings.HasPrefix(line, "data: ") {
			data := line[6:] // 去掉 "data: " 前缀
			if data == "[DONE]" {
				// 发送结束标记
				writer.Write([]byte("data: [DONE]\n\n"))
				break
			}
			// 转发 SSE 数据
			writer.Write([]byte("data: " + data + "\n\n"))
		}
		writer.Flush()
	}

	return scanner.Err()
}
