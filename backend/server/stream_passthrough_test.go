package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
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
		}, "\n")+"\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 1,
		Models: []config.ModelRef{openAIModel("m", upstream.URL)},
	}
	s := newTestServerWithStore(t, []config.ModelGroupConfig{group})

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
			`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"upstream","output":[],"created_at":1},"sequence_number":0}`,
			``,
			`event: response.reasoning_text.delta`,
			`data: {"type":"response.reasoning_text.delta","sequence_number":1,"item_id":"rs_1","output_index":0,"content_index":0,"delta":"thinking..."}`,
			``,
			`event: response.reasoning_text.done`,
			`data: {"type":"response.reasoning_text.done","sequence_number":2,"item_id":"rs_1","output_index":0,"content_index":0,"text":"thinking..."}`,
			``,
			`event: response.content_part.added`,
			`data: {"type":"response.content_part.added","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]},"sequence_number":3}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"42"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"upstream","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"42","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6},"created_at":1},"sequence_number":5}`,
			``,
		}, "\n")+"\n")
	}))
	defer upstream.Close()

	model := config.ModelRef{ID: "m", Name: "m", BaseURL: upstream.URL, Platform: "responses", APIKey: "k"}
	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 1,
		Models: []config.ModelRef{model},
	}
	s := newTestServerWithStore(t, []config.ModelGroupConfig{group})

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

	records := latestUsageRecords(t, s)
	if len(records) == 0 {
		t.Fatal("expected at least one usage record")
	}
	last := records[len(records)-1]
	if got := last.TotalTokens; got != 6 {
		t.Fatalf("usage total tokens = %d, want 6", got)
	}
}

// 取消发生在下游 flush 时，重现客户端看到终态就立即关闭连接的时序。
type cancelAfterFlushWriter struct {
	gin.ResponseWriter
	body   *httptest.ResponseRecorder
	marker string
	cancel context.CancelFunc
	ctx    context.Context
}

func (w *cancelAfterFlushWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(b)
}

func (w *cancelAfterFlushWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cancelAfterFlushWriter) Flush() {
	w.ResponseWriter.Flush()
	if strings.Contains(w.body.Body.String(), w.marker) {
		w.cancel()
	}
}

func TestStreamCancellationUsage(t *testing.T) {
	for _, endpoint := range []string{"chat", "responses", "converted", "custom"} {
		for _, completed := range []bool{false, true} {
			t.Run(endpoint+fmt.Sprint(completed), func(t *testing.T) {
				body := openAIChunk("c1", map[string]any{"content": "partial"}, "", nil)
				terminal := openAIChunk("c1", map[string]any{}, "stop", map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3})
				marker := `"finish_reason":"stop"`
				platform := "openai"
				if endpoint == "custom" {
					platform = "custom:openai-chat-completions"
				}
				if endpoint == "responses" || endpoint == "converted" {
					platform = "responses"
					body = "data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]},\"sequence_number\":0,\"output_index\":0,\"content_index\":0,\"item_id\":\"msg1\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"sequence_number\":1,\"output_index\":0,\"content_index\":0,\"item_id\":\"msg1\"}\n\n"
					terminal = `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial","annotations":[]}],"id":"item_0","status":"completed"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"id":"r1","model":"m","created_at":1,"object":"response"},"sequence_number":5}` + "\n\n"
					if endpoint == "responses" {
						marker = "response.completed"
					}
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, body)
					w.(http.Flusher).Flush()
					if completed {
						io.WriteString(w, terminal)
						if endpoint == "custom" {
							io.WriteString(w, openAIDone())
						}
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
				}))
				defer upstream.Close()
				model := openAIModel("actual", upstream.URL)
				model.Platform = platform
				s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 1, Models: []config.ModelRef{model}}})
				c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
				if endpoint == "responses" {
					rec = httptest.NewRecorder()
					c, _ = newResponsesContext(rec, `{"model":"grp","stream":true,"input":"hi"}`)
				}
				ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
				if !completed {
					marker = "partial"
				}
				c.Writer = &cancelAfterFlushWriter{c.Writer, rec, marker, cancel, ctx}
				if endpoint == "responses" {
					s.responses(c)
				} else {
					s.chatCompletions(c)
				}
				logs := latestUsageRecords(t, s)
				// The connection was canceled before transport completion. Converted
				// terminals remain pending until usage tails have been consumed.
				want := 499
				if len(logs) != 1 || logs[0].StatusCode != want {
					t.Fatalf("want %d: %+v", want, logs)
				}
				var record usageRecord
				if err := json.Unmarshal([]byte(storedRecordJSON(t, s.store, logs[0].RequestID)), &record); err != nil {
					t.Fatal(err)
				}
				if record.ErrorKind != ErrorKindClientCanceled {
					t.Fatalf("errorKind=%q", record.ErrorKind)
				}
				if completed && (record.ProtocolUsage == nil || record.ProtocolUsage.Total == nil || record.ProtocolUsage.Total.Count != 3) {
					t.Fatalf("observed usage lost on cancellation: %+v", record.ProtocolUsage)
				}
			})
		}
	}
}

type flushErrorResponseWriter struct{ *httptest.ResponseRecorder }

func (w *flushErrorResponseWriter) FlushError() error { return io.ErrClosedPipe }

func TestStreamFlushErrorThroughGinAndCapture(t *testing.T) {
	c, _ := gin.CreateTestContext(&flushErrorResponseWriter{httptest.NewRecorder()})
	installDownstreamCapture(c, &usageRecord{}, 0)
	w := &ginStreamWriter{writer: c.Writer}
	if err := w.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost underlying flush error: %v", err)
	}
}

func TestToolOnlyStreamWithoutUsageSucceeds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"id":"r1","model":"m","created":1,"object":"chat.completion.chunk"}`+"\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 1, Models: []config.ModelRef{openAIModel("m", upstream.URL)}}})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != 200 || !strings.Contains(rec.Body.String(), "call_1") {
		t.Fatalf("tool completion: %+v", logs)
	}
}

func TestCanceledConnectionDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	model := openAIModel("m", upstream.URL)
	model.Platform = "responses"
	s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 2, Models: []config.ModelRef{model}}})
	c, _ := newResponsesContext(httptest.NewRecorder(), `{"model":"grp","stream":true,"input":"hi"}`)
	c.Request = c.Request.WithContext(ctx)
	s.responses(c)
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != 499 || calls.Load() != 1 {
		t.Fatalf("calls=%d logs=%+v", calls.Load(), logs)
	}
}
