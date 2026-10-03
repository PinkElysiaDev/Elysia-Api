package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func dashscopeStreamDefinition(t *testing.T) protocol.Definition {
	t.Helper()
	definition := cumulativeWireDefinition(t)
	definition.ID, definition.Family = "dashscope-native", "dashscope-native"
	operation := definition.Operations["generate"]
	operation.Path = "/api/v1/services/aigc/text-generation/generation"
	operation.Headers["X-DashScope-SSE"] = "enable"
	operation.Framing.Done = []string{"[DONE]"}
	definition.Operations["generate"] = operation
	mapping := definition.Directions[protocol.DecodeEvent]
	raw, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), `"path":"/payload/text"`, `"path":"/output/choices/0/message/content"`))
	raw = []byte(strings.ReplaceAll(string(raw), `{"op":"read","path":"/finished"}`, `{"op":"equal","items":[{"op":"read","path":"/output/choices/0/finish_reason"},{"op":"literal","value":"stop"}]}`))
	if err := json.Unmarshal(raw, &mapping); err != nil {
		t.Fatal(err)
	}
	snapshot := mapping.Rules[2].Emit.Items[0].Then.Items[0]
	item := snapshot.Fields["item"]
	item.Fields["payload"] = protocol.Expression{Op: "join", Source: &protocol.Expression{Op: "map",
		Source: &protocol.Expression{Op: "read", Path: "/output/choices/0/message/content", Required: true},
		Body:   &protocol.Expression{Op: "read", From: "item", Path: "/text", Required: true}}}
	snapshot.Fields["item"] = item
	mapping.Rules[2].Emit.Items[0].Then.Items[0] = snapshot
	definition.Directions[protocol.DecodeEvent] = mapping
	for index, sample := range definition.Samples {
		if sample.Direction == protocol.DecodeEvent {
			sample.Input = mustProtocolValue(t, `[
			 {"output":{"choices":[{"message":{"content":[{"text":"hel"}]}}]}},
			 {"output":{"choices":[{"message":{"content":[{"text":"hel"},{"text":"lo"}]}}]}},
			 {"output":{"choices":[{"message":{"content":[{"text":"hello!"}]},"finish_reason":"stop"}]},"usage":{"input_tokens":2,"output_tokens":3}}
			]`)
			definition.Samples[index] = sample
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
	groups[0].Models[0].Name = "qwen-plus"
	s := newTestServer(t, groups, definition)
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"你好"}]}`)
	s.chatCompletions(c)
	if gotHeader != "enable" || gotPath != definition.Operations["generate"].Path || !strings.Contains(gotBody, `"qwen-plus"`) {
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
