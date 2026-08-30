package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type captureStreamWriter struct {
	builder strings.Builder
	flushes int
}

func (w *captureStreamWriter) Write(data []byte) (int, error) {
	return w.builder.Write(data)
}

func (w *captureStreamWriter) WriteString(data string) (int, error) {
	return w.builder.WriteString(data)
}

func (w *captureStreamWriter) Flush() error {
	w.flushes++
	return nil
}

func (w *captureStreamWriter) String() string {
	return w.builder.String()
}

// sseResponse 把 SSE 文本包装成 *http.Response 供流式管线测试使用。
func sseResponse(body string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(body))}
}

func TestForwardResponsesStreamPreservesEventStreamLines(t *testing.T) {
	resp := sseResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	writer := &captureStreamWriter{}

	if err := ForwardResponsesStream(context.Background(), resp, writer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := writer.String(); got != "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" {
		t.Fatalf("expected stream to be forwarded verbatim, got %q", got)
	}
	// 一次空行 flush + 循环结束的兜底 flush（确保末尾事件不被滞留，
	// 修复 codex "stream closed before response.completed"）。
	if writer.flushes < 1 {
		t.Fatalf("expected at least one flush, got %d", writer.flushes)
	}
}

// 回归 #2：上游最后一个事件后没有紧跟空行就 EOF 时，末尾事件必须被 flush 给下游，
// 否则 codex 报 "stream closed before response.completed"。
func TestForwardResponsesStreamFlushesFinalEventWithoutTrailingBlank(t *testing.T) {
	// 注意：结尾没有 \n\n（缺少收尾空行）。
	resp := sseResponse("event: response.completed\ndata: {\"type\":\"response.completed\"}")
	writer := &captureStreamWriter{}

	if err := ForwardResponsesStream(context.Background(), resp, writer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(writer.String(), "response.completed") {
		t.Fatalf("final event must be forwarded, got %q", writer.String())
	}
	if writer.flushes < 1 {
		t.Fatalf("final event must be flushed to client, got %d flushes", writer.flushes)
	}
}
