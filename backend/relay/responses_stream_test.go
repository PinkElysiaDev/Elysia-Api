package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cancelOnFlushWriter struct {
	captureStreamWriter
	cancel   context.CancelFunc
	ctx      context.Context
	marker   string
	writeErr error
	flushErr error
}

func (w *cancelOnFlushWriter) WriteString(s string) (int, error) {
	if w.ctx != nil && w.ctx.Err() != nil {
		return 0, w.ctx.Err()
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.captureStreamWriter.WriteString(s)
}

func (w *cancelOnFlushWriter) Flush() error {
	if w.ctx != nil && w.ctx.Err() != nil {
		return w.ctx.Err()
	}
	w.captureStreamWriter.Flush()
	if strings.Contains(w.String(), w.marker) {
		if w.cancel != nil {
			w.cancel()
		}
		return w.flushErr
	}
	return nil
}

func TestTransformedStreamTerminalThenDisconnect(t *testing.T) {
	body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for format, marker := range map[FormatType]string{
		FormatOpenAIChat: `"finish_reason":"stop"`, FormatResponses: "response.completed",
		FormatClaude: "message_stop", FormatGemini: `"finishReason":"STOP"`,
	} {
		t.Run(string(format), func(t *testing.T) {
			for _, flushErr := range []error{nil, io.ErrClosedPipe} {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				w := &cancelOnFlushWriter{ctx: ctx, cancel: cancel, marker: marker, flushErr: flushErr}
				err := TransformStreamViaMaheshvara(ctx, sseResponse(body), FormatOpenAIChat, format, w, "m")
				if (err != nil) != (flushErr != nil) {
					t.Fatalf("terminal flush=%v, result=%v", flushErr, err)
				}
				if !strings.Contains(w.String(), marker) || strings.Contains(w.String(), `"error"`) {
					t.Fatalf("invalid completion: %s", w.String())
				}
			}
		})
	}
}

func TestPassthroughTerminalThenCancel(t *testing.T) {
	for _, responses := range []bool{true, false} {
		t.Run(fmt.Sprint(responses), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"
			forward := ForwardOpenAIStream
			if responses {
				body = "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
				forward = ForwardResponsesStream
			}
			w := &cancelOnFlushWriter{cancel: cancel}
			if err := forward(ctx, sseResponse(body), w); err != nil {
				t.Fatalf("completed stream: %v", err)
			}
			if w.String() != body {
				t.Fatalf("changed raw SSE: %q", w.String())
			}
		})
	}
}

func TestPassthroughIncompleteAndWriteFailure(t *testing.T) {
	boom := errors.New("downstream write failed")
	for _, tc := range []struct {
		name, body         string
		writeErr, flushErr error
	}{
		{"incomplete", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", nil, nil},
		{"write", "data: {\"type\":\"response.completed\"}\n\n", boom, nil},
		{"flush", "data: {\"type\":\"response.completed\"}\n\n", nil, boom},
		{"upstream failure", "data: {\"type\":\"response.failed\"}\n\n", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &cancelOnFlushWriter{writeErr: tc.writeErr, flushErr: tc.flushErr}
			if err := ForwardResponsesStream(context.Background(), sseResponse(tc.body), w); err == nil {
				t.Fatal("must fail")
			}
		})
	}
}

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
	resp := sseResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	writer := &captureStreamWriter{}

	if err := ForwardResponsesStream(context.Background(), resp, writer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := writer.String(); got != "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n" {
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
