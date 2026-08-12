package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
)

// 回归：OpenAI 系同协议透传时，上游 SSE 必须原样转发，provider 私有字段
// 与 tool call id 不能被 Maheshvara 重渲染丢弃。
func TestOpenAIChatPassthroughStreamForwardsRawSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"cmpl-1","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"lookup","arguments":""}}]},"finish_reason":null}],"x_provider":"raw_marker"}`,
			``,
			`data: {"id":"cmpl-1","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			``,
			`data: {"id":"cmpl-1","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 1,
		Models: []config.ModelRef{openAIModel("m", upstream.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 stream, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"x_provider":"raw_marker"`,
		`"id":"call_9"`,
		`"prompt_tokens":2`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("raw passthrough stream lost %q, got:\n%s", want, body)
		}
	}
}

// 回归：Responses 同协议透传时，请求体里的 reasoning_text 输入项必须原样
// 到达上游，上游 SSE 里的 response.reasoning_text.* 事件必须原样到达下游，
// 且 usage 仍被正确记录。
func TestResponsesPassthroughStreamPreservesReasoningText(t *testing.T) {
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		upstreamBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"upstream","output":[]}}`,
			``,
			`event: response.reasoning_text.delta`,
			`data: {"type":"response.reasoning_text.delta","sequence_number":1,"item_id":"rs_1","output_index":0,"content_index":0,"delta":"thinking..."}`,
			``,
			`event: response.reasoning_text.done`,
			`data: {"type":"response.reasoning_text.done","sequence_number":2,"item_id":"rs_1","output_index":0,"content_index":0,"text":"thinking..."}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"42"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"upstream","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"42","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()

	model := config.ModelRef{ID: "m", Name: "m", BaseURL: upstream.URL, Platform: "responses", APIKey: "k"}
	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 1,
		Models: []config.ModelRef{model},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	requestBody := `{"model":"grp","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"reasoning_text","text":"prior thought"}]}`
	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, requestBody)
	s.responses(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 stream, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: response.reasoning_text.delta",
		`"delta":"thinking..."`,
		"event: response.reasoning_text.done",
		"event: response.output_text.delta",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("responses passthrough stream lost %q, got:\n%s", want, body)
		}
	}
	if !strings.Contains(upstreamBody, `"reasoning_text"`) {
		t.Fatalf("upstream request lost reasoning_text input item, got: %s", upstreamBody)
	}

	records := s.usageSnapshot()
	if len(records) == 0 {
		t.Fatal("expected at least one usage record")
	}
	last := records[len(records)-1]
	if got := getInt(last.Usage.TotalTokens); got != 6 {
		t.Fatalf("usage total tokens = %d, want 6", got)
	}
}
