package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func dashscopeStreamDefinition(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	definition := cumulativeWireDefinition(t)
	definition.Spec.ID, definition.Spec.Family = "dashscope-native", "dashscope-native"
	operation := definition.Spec.Operations["generate"]
	operation.Path = "/api/v1/services/aigc/text-generation/generation"
	operation.Headers["X-DashScope-SSE"] = "enable"
	operation.Framing.Done = []string{"[DONE]"}
	definition.Spec.Operations["generate"] = operation
	content := protocol.Expression{Op: "read", Path: "/output/choices/0/message/content"}
	text := protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &content}, Then: &protocol.Expression{Op: "join", Source: &protocol.Expression{Op: "map", Source: &content, Body: &protocol.Expression{Op: "read", From: "item", Path: "/text", Required: true}}}}
	finished := protocol.Expression{Op: "equal", Items: []protocol.Expression{{Op: "read", Path: "/output/choices/0/finish_reason"}, {Op: "literal", Value: protocol.StringValue("stop")}}}
	definition.Mapping.Expressions["cumulative-text"], definition.Spec.Expressions["cumulative-text"] = text, text
	definition.Mapping.Expressions["cumulative-finished"], definition.Spec.Expressions["cumulative-finished"] = finished, finished
	for index, sample := range definition.Mapping.Samples {
		if sample.Direction == protocol.DecodeEvent {
			sample.Input = mustProtocolValue(t, `[
			 {"output":{"choices":[{"message":{"content":[{"text":"hel"}]}}]}},
			 {"output":{"choices":[{"message":{"content":[{"text":"hel"},{"text":"lo"}]}}]}},
			 {"output":{"choices":[{"message":{"content":[{"text":"hello!"}]},"finish_reason":"stop"}]},"usage":{"input_tokens":2,"output_tokens":3}}
			]`)
			definition.Mapping.Samples[index] = sample
		}
	}
	return definition
}

// The provider-specific cumulative path and array text blocks are entirely
// declarative. Multibyte prefixes, headers and trailing counters reach HTTP.
func TestChatCompletionsDashscopeNativeStreamingEndToEnd(t *testing.T) {
	definition := dashscopeStreamDefinition(t)
	var gotBody, gotHeader, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody, gotHeader, gotPath = string(body), r.Header.Get("X-DashScope-SSE"), r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"output":{"choices":[{"message":{"content":[{"text":"你好"}]}}]},"usage":{"input_tokens":10,"output_tokens":5}}`+"\n\n")
		fmt.Fprint(w, `data: {"output":{"choices":[{"message":{"content":[{"text":"你好"},{"text":"，世界"}]},"finish_reason":"stop"}]},"usage":{"input_tokens":10,"output_tokens":8}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	groups := standaloneGroup(t, definition, upstream.URL)
	groups[0].Models[0].ID = "qwen-plus"
	s := newTestServer(t, groups, definition)
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"你好"}]}`)
	s.chatCompletions(contextForTest(c), c)
	flushTestResponse(c)
	if gotHeader != "enable" || gotPath != definition.Spec.Operations["generate"].Path || !strings.Contains(gotBody, `"qwen-plus"`) {
		t.Fatal(gotHeader, gotPath, gotBody)
	}
	for _, want := range []string{`"content":"你好"`, `"content":"，世界"`, `"prompt_tokens":10`, `"completion_tokens":8`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatal(want, rec.Body)
		}
	}
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].TotalTokens != 18 || strings.Contains(rec.Body.String(), `"content":"你好，世界"`) {
		t.Fatal(logs, rec.Body)
	}
}
