package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestProtocolEstimateCountsUnicodeFilesAndToolHistoryOnce(t *testing.T) {
	server := &Server{config: &config.Config{Usage: config.UsageConfig{CharsPerToken: 1, DefaultOutputTokenEstimate: 100, ImageInputTokenEstimate: 30, FileInputTokenEstimatePerKB: 10}}}
	request := &protocol.Request{Content: []protocol.Node{
		{Kind: protocol.MessageNode, Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("你好")}, {Kind: protocol.ImageNode}, {Kind: protocol.DocumentNode, Payload: mustEncodedProtocolValue(t, protocol.Object{"data": protocol.StringValue(strings.Repeat("a", 2049))})}}},
		{Kind: protocol.ToolResultNode, Payload: mustProtocolValue(t, `[{"type":"text","text":"done"}]`), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("done")}}},
		{Kind: protocol.ToolCallNode, Name: protocol.StringValue("shell"), Input: &protocol.ToolInput{Kind: protocol.TextInput, Value: protocol.StringValue("ls")}},
	}, Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, `7`)}}
	if actual := server.estimateProtocolInputTokens(request); actual != 73 {
		t.Fatalf("input estimate = %d, want 2+30+30+4+5+2", actual)
	}
	if actual := server.estimateProtocolTokens(request); actual != 80 {
		t.Fatalf("explicit output budget not respected: %d", actual)
	}
	request.Tools = []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("f"), InputSchema: mustProtocolValue(t, `{"type":"object"}`)}}
	if actual := server.estimateProtocolInputTokens(request); actual <= 73 {
		t.Fatal("tool definition missing from estimate")
	}
}

func TestProtocolEstimateIncludesStructuredReasoningHistory(t *testing.T) {
	s := &Server{config: &config.Config{Usage: config.UsageConfig{CharsPerToken: 1}}}
	request := &protocol.Request{Content: []protocol.Node{{Kind: protocol.ReasoningNode, ReasoningForm: protocol.StructuredReasoning,
		Children:         []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("摘要")}},
		ReasoningContent: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("first")}, {Kind: protocol.TextNode, Payload: protocol.StringValue("second")}},
	}}}
	if got := s.estimateProtocolInputTokens(request); got != 13 {
		t.Fatalf("reasoning history estimate = %d, want 2+5+6", got)
	}
}

func TestHostedToolAccountingDeduplicatesAndHonorsSource(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "openai-responses"))
	record := &usageRecord{}
	for _, wire := range []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call"}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"web_search_call"}}`,
		`{"type":"response.completed","response":{"output":[{"type":"web_search_call"},{"type":"web_search_call"},{"type":"file_search_call"}]}}`,
	} {
		if err := observeHostedTools(record, compiled, []byte(wire)); err != nil {
			t.Fatal(err)
		}
	}
	if record.BuiltinToolUsage.WebSearchCalls != 2 || record.BuiltinToolUsage.FileSearchCalls != 1 {
		t.Fatal("snapshots double counted", record.BuiltinToolUsage)
	}
	updateRecordProtocolUsage(record, &protocol.Usage{Details: map[string]protocol.Counter{"tools.web_search_calls": {Count: 0, Origin: protocol.ObservedCount}}})
	if record.BuiltinToolUsage.WebSearchCalls != 0 {
		t.Fatal("explicit mapped zero did not override native observation")
	}
	counter := builtin.NewToolAccounting(1)
	wire := []byte(`{"output":[{"type":"web_search_call"},{"type":"web_search_call"}]}`)
	if err := counter.Observe(protocol.Identity{Family: "unrelated"}, wire); err != nil || counter.Count("web_search") != 0 {
		t.Fatal("type guessed protocol source", err)
	}
	if err := counter.Observe(compiled.Identity(), wire); err == nil {
		t.Fatal("unbounded correlation storage")
	}
}

func TestHostedToolUsagePersistedHTTPAndSSE(t *testing.T) {
	for _, isStream := range []bool{false, true} {
		t.Run(fmt.Sprint(isStream), func(t *testing.T) {
			payload := `{"status":"completed","output":[{"type":"web_search_call","id":"s1","status":"completed"},{"type":"web_search_call","id":"s2","status":"completed"}],"usage":{"input_tokens":9,"output_tokens":2},"id":"r1","model":"m","object":"response","created_at":1}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !isStream {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(payload))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"r1\",\"model\":\"m\",\"object\":\"response\",\"created_at\":1,\"status\":\"in_progress\",\"output\":[]}}\n\n")
				fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"web_search_call\",\"id\":\"s1\",\"status\":\"completed\"},\"sequence_number\":1}\n\n")
				fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":%s}\n\n", payload)
			}))
			defer upstream.Close()
			server := newTestServer(t, presetGroup(t, "custom:openai-responses", upstream.URL))
			request, recorder := chatRequestContext(fmt.Sprintf(`{"model":"grp","input":"hello","stream":%t}`, isStream))
			request.Request.URL.Path = "/v1/responses"
			request.Request.Header.Set("X-Elysia-Request-Id", "untrusted-client-id")
			server.responses(request)
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "s2") {
				t.Fatalf("native hosted tools failed: %d %s", recorder.Code, recorder.Body)
			}
			items := latestUsageRecords(t, server)
			if len(items) != 1 || items[0].Error != "" {
				t.Fatalf("unexpected usage: %+v", items)
			}
			if id := recorder.Header().Get("X-Elysia-Request-Id"); id != items[0].RequestID || id == "untrusted-client-id" {
				t.Fatalf("response request ID %q does not identify its persisted call %q", id, items[0].RequestID)
			}
			body, exists, err := server.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
			if err != nil || !exists {
				t.Fatal("usage detail not persisted", err)
			}
			var saved usageRecord
			if err := json.Unmarshal(body, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.BuiltinToolUsage.WebSearchCalls != 2 {
				t.Fatalf("hosted tools lost or duplicated in persisted statistics: %+v", saved.BuiltinToolUsage)
			}
		})
	}
}

func TestRejectedRequestIDIdentifiesPersistedCall(t *testing.T) {
	server := newTestServer(t, presetGroup(t, "custom:openai-responses", "http://127.0.0.1:1"))
	request, recorder := chatRequestContext(`{"model":"grp","input":"hello","store":42}`)
	request.Request.URL.Path = "/v1/responses"
	server.responses(request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid input accepted: %d %s", recorder.Code, recorder.Body)
	}
	items := latestUsageRecords(t, server)
	if len(items) != 1 || items[0].RequestID != recorder.Header().Get("X-Elysia-Request-Id") || items[0].Error == "" {
		t.Fatalf("rejected call cannot be correlated: header=%q records=%+v", recorder.Header().Get("X-Elysia-Request-Id"), items)
	}
}
