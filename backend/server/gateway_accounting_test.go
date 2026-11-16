package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestProtocolEstimateCountsUnicodeFilesAndToolHistoryOnce(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := &Server{config: &config.Config{Usage: config.UsageConfig{CharsPerToken: 1}}}
	request := &protocol.Request{Content: []protocol.Node{{Kind: protocol.ReasoningNode, ReasoningForm: protocol.StructuredReasoning,
		Children:         []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("摘要")}},
		ReasoningContent: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("first")}, {Kind: protocol.TextNode, Payload: protocol.StringValue("second")}},
	}}}
	if got := s.estimateProtocolInputTokens(request); got != 13 {
		t.Fatalf("reasoning history estimate = %d, want 2+5+6", got)
	}
}

func TestHostedToolAccountingUsesMappedSnapshots(t *testing.T) {
	t.Parallel()
	compiled := compileFixturePackage(t, presetPackage(t, protocol.PresetResponsesID))
	wire := []byte(`{"id":"r","object":"response","model":"m","created_at":1,"status":"completed","output":[{"type":"web_search_call","id":"s1","status":"completed"},{"type":"web_search_call","id":"s2","status":"completed"},{"type":"file_search_call","id":"f1","status":"completed","queries":["q"]}],"usage":{"input_tokens":9,"output_tokens":2}}`)
	record := &usageRecord{}
	for range 2 {
		response, err := compiled.DecodeResponse(t.Context(), wire, protocol.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		updateRecordProtocolUsage(record, response.Usage)
	}
	if record.BuiltinToolUsage.WebSearchCalls != 2 || record.BuiltinToolUsage.FileSearchCalls != 1 {
		t.Fatal("snapshots double counted", record.BuiltinToolUsage)
	}
	updateRecordProtocolUsage(record, &protocol.Usage{Details: map[string]protocol.Counter{"tools.web_search_calls": {Count: 0, Origin: protocol.ObservedCount}}})
	if record.BuiltinToolUsage.WebSearchCalls != 0 {
		t.Fatal("observed zero did not replace inferred tool count")
	}
}

func TestHostedToolUsagePersistedHTTPAndSSE(t *testing.T) {
	t.Parallel()
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
				for index, id := range []string{"s1", "s2"} {
					fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":%d,\"item\":{\"type\":\"web_search_call\",\"id\":%q,\"status\":\"in_progress\"},\"sequence_number\":%d}\n\n", index, id, index*2+1)
					fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":{\"type\":\"web_search_call\",\"id\":%q,\"status\":\"completed\"},\"sequence_number\":%d}\n\n", index, id, index*2+2)
				}
				fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":%s}\n\n", payload)
			}))
			defer upstream.Close()
			server := newTestServer(t, presetGroup(t, "custom:openai-responses", upstream.URL))
			request, recorder := chatRequestContext(fmt.Sprintf(`{"model":"grp","input":"hello","stream":%t}`, isStream))
			request.Request.URI().SetPath("/v1/responses")
			request.Request.Header.Set("X-Elysia-Request-Id", "untrusted-client-id")
			server.responses(contextForTest(request), request)
			flushTestResponse(request)
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
			if err := sonic.ConfigStd.Unmarshal(body, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.BuiltinToolUsage.WebSearchCalls != 2 {
				t.Fatalf("hosted tools lost or duplicated in persisted statistics: %+v", saved.BuiltinToolUsage)
			}
		})
	}
}

func TestRejectedRequestIDIdentifiesPersistedCall(t *testing.T) {
	t.Parallel()
	server := newTestServer(t, presetGroup(t, "custom:openai-responses", "http://127.0.0.1:1"))
	request, recorder := chatRequestContext(`{"model":"grp","input":"hello","store":42}`)
	request.Request.URI().SetPath("/v1/responses")
	server.responses(contextForTest(request), request)
	flushTestResponse(request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid input accepted: %d %s", recorder.Code, recorder.Body)
	}
	items := latestUsageRecords(t, server)
	if len(items) != 1 || items[0].RequestID != recorder.Header().Get("X-Elysia-Request-Id") || items[0].Error == "" {
		t.Fatalf("rejected call cannot be correlated: header=%q records=%+v", recorder.Header().Get("X-Elysia-Request-Id"), items)
	}
}

func TestGatewayReservationUsesExecutableRetryWindow(t *testing.T) {
	t.Parallel()
	s := &Server{config: &config.Config{Usage: config.UsageConfig{CharsPerToken: 1, DefaultOutputTokenEstimate: 10}}}
	request := func(limit string) *protocol.Request {
		return &protocol.Request{Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, limit)}}
	}
	plan := &gatewayPlan{group: &config.ModelGroupConfig{MaxRetries: 1}, request: request("1"), candidates: []gatewayCandidate{{prepared: request("100")}, {prepared: request("200")}, {prepared: request("9000")}}}
	if got := s.estimateGatewayReservation(plan); got != 200 {
		t.Fatal(got)
	}
	plan.group.MaxRetries = 0
	if got := s.estimateGatewayReservation(plan); got != 100 {
		t.Fatal(got)
	}
}
