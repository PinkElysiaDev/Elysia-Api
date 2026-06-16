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
		// codex 要求 response.completed.usage 含整数 input_tokens/output_tokens/total_tokens
		`"input_tokens":3`,
		`"output_tokens":2`,
		`"total_tokens":5`,
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

// R1 回归：Chat→Responses 流式必须为工具调用补发 output_item.added(function_call)、
// function_call_arguments.done，并把 function_call 项纳入 response.completed.output。
func TestConvertOpenAIChatStreamToResponsesStreamEmitsFunctionCallItem(t *testing.T) {
	resp := sseResponse(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"))
	writer := &captureStreamWriter{}

	if err := ConvertOpenAIChatStreamToResponsesStream(resp, writer, "gpt-4o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := writer.String()
	for _, want := range []string{
		"event: response.output_item.added",
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		`"name":"lookup"`,
		"event: response.function_call_arguments.delta",
		"event: response.function_call_arguments.done",
		`"arguments":"{\"q\":\"x\"}"`,
		"event: response.output_item.done",
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
		// 重映射 + 合并语义：input_tokens(10) 来自 message_start，output_tokens(4) 来自
		// message_delta，二者必须都保留（修复前 message_delta 会覆盖丢掉 input_tokens）。
		`"input_tokens":10`,
		`"output_tokens":4`,
		`"total_tokens":14`,
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
		// Gemini usageMetadata 重映射：promptTokenCount→input, candidatesTokenCount→output。
		`"input_tokens":5`,
		`"output_tokens":6`,
		`"total_tokens":11`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
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
