package relay

import (
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

func sseResponse(body string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(body))}
}

func TestConvertOpenAIChatStreamToResponsesStreamEmitsTextAndUsage(t *testing.T) {
	resp := sseResponse(strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hel"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n"))
	writer := &captureStreamWriter{}

	if err := ConvertOpenAIChatStreamToResponsesStream(resp, writer, "gpt-4o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := writer.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		`"delta":"hel"`,
		`"delta":"lo"`,
		"event: response.output_text.done",
		`"text":"hello"`,
		"event: response.output_item.done",
		"event: response.completed",
		`"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if writer.flushes == 0 {
		t.Fatal("expected stream writer to be flushed")
	}
}

func TestConvertOpenAIChatStreamToResponsesStreamEmitsFunctionArgumentDeltas(t *testing.T) {
	resp := sseResponse(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":":\"x\"}"}}]}}]}`,
		`data: [DONE]`,
		``,
	}, "\n"))
	writer := &captureStreamWriter{}

	if err := ConvertOpenAIChatStreamToResponsesStream(resp, writer, "gpt-4o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := writer.String()
	for _, want := range []string{
		"event: response.function_call_arguments.delta",
		`"item_id":"call_1"`,
		`"delta":"{\"q\""`,
		`"delta":":\"x\"}"`,
		"event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestConvertClaudeStreamToResponsesStreamEmitsTextAndUsage(t *testing.T) {
	resp := sseResponse(strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":2}}}`,
		`data: {"type":"content_block_delta","delta":{"text":"hi"}}`,
		`data: {"type":"message_delta","usage":{"output_tokens":4}}`,
		`data: [DONE]`,
		``,
	}, "\n"))
	writer := &captureStreamWriter{}

	if err := ConvertClaudeStreamToResponsesStream(resp, writer, "claude-3-5"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := writer.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		`"delta":"hi"`,
		"event: response.output_text.done",
		`"text":"hi"`,
		"event: response.completed",
		`"usage":{"output_tokens":4}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestConvertGeminiStreamToResponsesStreamEmitsTextAndUsage(t *testing.T) {
	resp := sseResponse(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"gem"},{"text":"ini"}]}}]}`,
		`data: {"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6,"totalTokenCount":11}}`,
		``,
	}, "\n"))
	writer := &captureStreamWriter{}

	if err := ConvertGeminiStreamToResponsesStream(resp, writer, "gemini-2.5"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := writer.String()
	for _, want := range []string{
		"event: response.output_text.delta",
		`"delta":"gemini"`,
		"event: response.output_text.done",
		`"text":"gemini"`,
		"event: response.completed",
		`"usage":{"candidatesTokenCount":6,"promptTokenCount":5,"totalTokenCount":11}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestForwardResponsesStreamPreservesEventStreamLines(t *testing.T) {
	resp := sseResponse("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	writer := &captureStreamWriter{}

	if err := ForwardResponsesStream(resp, writer); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := writer.String(); got != "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" {
		t.Fatalf("expected stream to be forwarded verbatim, got %q", got)
	}
	if writer.flushes != 1 {
		t.Fatalf("expected one flush on blank line, got %d", writer.flushes)
	}
}
