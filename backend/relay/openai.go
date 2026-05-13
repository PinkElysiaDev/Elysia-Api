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
	if timeout > 0 {
		client.Timeout = timeout
	}
	return &OpenAIAdapter{client: client}
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
