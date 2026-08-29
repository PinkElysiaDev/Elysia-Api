package relay

import (
	"context"
	"strings"
	"testing"
)

// 批次二回归：流式终态语义——[DONE] 无 finish 判失败、finish_reason:error
// 判终态失败、终态 message 快照只补缺失后缀、恰一个终态帧。

func chatStream(t *testing.T, lines []string, target FormatType) (string, error) {
	t.Helper()
	body := strings.Join(lines, "\n")
	writer := &captureStreamWriter{}
	err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatOpenAIChat, target, writer, "m")
	return writer.String(), err
}

// 已见过 choice 却从未收到 finish_reason 的流，[DONE] 不是终态替身——
// 必须判定失败，而不是把半截输出当完整答案交付。
func TestChatStreamDoneWithoutFinishFails(t *testing.T) {
	out, err := chatStream(t, []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"半截"}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, FormatOpenAIChat)
	if err == nil {
		t.Fatalf("stream without finish_reason must fail, got success:\n%s", out)
	}
	if !strings.Contains(out, "upstream_stream_error") && !strings.Contains(out, "error") {
		t.Fatalf("downstream should receive an error payload:\n%s", out)
	}
}

// finish_reason:"error" 是终态失败，不得伪装成正常 stop（DC5b）。
func TestChatStreamFinishReasonErrorIsFailure(t *testing.T) {
	out, err := chatStream(t, []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"答案"},"finish_reason":"error"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, FormatOpenAIChat)
	if err == nil {
		t.Fatalf("finish_reason=error must fail the stream:\n%s", out)
	}
	if strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("error must not masquerade as stop:\n%s", out)
	}
}

// 终态 chunk 用完整 message 回传（快照语义）：已流式输出过的内容不重发，
// 只补缺失后缀（PC7f）。
func TestChatStreamTerminalSnapshotSuffixOnly(t *testing.T) {
	out, err := chatStream(t, []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"Hello World"},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, FormatOpenAIChat)
	if err != nil {
		t.Fatalf("stream should succeed: %v\n%s", err, out)
	}
	if got := strings.Count(out, `"content":"Hello"`); got != 1 {
		t.Fatalf("streamed prefix should appear exactly once, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, `"content":" World"`); got != 1 {
		t.Fatalf("snapshot suffix should be emitted exactly once, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, `"finish_reason":"stop"`); got != 1 {
		t.Fatalf("exactly one terminal frame expected, got %d:\n%s", got, out)
	}
}

// 终态快照里的完整工具参数：与已流式内容前缀一致时只补后缀，不重复拼接。
func TestChatStreamTerminalSnapshotToolArguments(t *testing.T) {
	out, err := chatStream(t, []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"alpha","arguments":"{\"a\":"}}]}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"alpha","arguments":"{\"a\":1}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, FormatOpenAIChat)
	if err != nil {
		t.Fatalf("stream should succeed: %v\n%s", err, out)
	}
	if got := strings.Count(out, `"arguments":"1}"`); got != 1 {
		t.Fatalf("snapshot arguments should render as suffix once, got %d:\n%s", got, out)
	}
	if strings.Contains(out, `{"a":{"a":1}`) {
		t.Fatalf("arguments must not be duplicated:\n%s", out)
	}
}

// 多个 completed 事件（上游重发终态）只渲染一个终态帧（PC7d）。
func TestChatStreamExactlyOneTerminalFrame(t *testing.T) {
	out, err := chatStream(t, []string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, FormatOpenAIChat)
	if err != nil {
		t.Fatalf("stream should succeed: %v\n%s", err, out)
	}
	if got := strings.Count(out, `"finish_reason":"stop"`); got != 1 {
		t.Fatalf("exactly one terminal frame expected, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "[DONE]"); got != 1 {
		t.Fatalf("exactly one [DONE] expected, got %d:\n%s", got, out)
	}
}
