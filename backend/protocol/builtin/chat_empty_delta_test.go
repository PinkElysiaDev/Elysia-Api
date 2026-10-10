package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestChatEmptyTextDeltasDoNotCreateHistoryItems(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	frames := []string{
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"visible"},"finish_reason":null}]}`,
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	starts := map[p.NodeKind]int{}
	for _, raw := range frames {
		frame, err := chat.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if event.Type == p.ItemStarted && event.Item != nil {
				starts[event.Item.Kind]++
			}
		}
		wire, err := chat.EncodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(wire) != 1 {
			t.Fatal("native frame count changed", len(wire))
		}
		sameJSON(t, wire[0].Bytes(), raw)
	}
	if starts[p.TextNode] != 0 || starts[p.ReasoningNode] != 1 || starts[p.ToolCallNode] != 1 {
		t.Fatal("empty deltas minted phantom history", starts)
	}
}
