package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestResponsesSeparateToolLedgerPersistedAcrossClientProjection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			const ledger = `{"image_gen":{"input_tokens":7,"output_tokens":5,"total_tokens":12,"input_tokens_details":{"image_tokens":4,"text_tokens":3},"output_tokens_details":{"image_tokens":5,"text_tokens":0}},"web_search":{"num_requests":2}}`
			const item = `{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}`
			const attribution = `{"items":{"msg":{"input_tokens":2,"output_tokens":1,"content":[{"input_tokens":2,"output_tokens":1}]}},"request_fields":{"instructions":{"input_tokens":1,"cached_tokens":0}}}`
			const billing = `{"basis":"disclosed_billing_allocation","billed_cached_tokens":1,"observed_cached_tokens":3,"policy_revision":2,"rule_id":"test-rule"}`
			const response = `{"object":"response","id":"r","model":"m","created_at":1,"service_tier":"default","status":"completed","output":[` + item + `],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4,"input_tokens_details":{"cached_tokens":1},"attribution":` + attribution + `,"linapi_cache_billing":` + billing + `},"tool_usage":` + ledger + `}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				frames := []string{
					`{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"service_tier":"auto","status":"in_progress","output":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
					`{"type":"response.in_progress","response":{"id":"r","service_tier":"auto","status":"in_progress","output":[],"tool_usage":` + ledger + `}}`,
					`{"type":"response.in_progress","response":{"id":"r","status":"in_progress","output":[],"tool_usage":` + ledger + `}}`,
					`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
					`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"OK"}`,
					`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"OK","annotations":[]}}`,
					`{"type":"response.completed","response":` + response + `}`,
				}
				for _, frame := range frames {
					fmt.Fprintf(w, "data: %s\n\n", frame)
				}
			}))
			defer upstream.Close()
			s := newTestServer(t, presetGroup(t, "custom:openai-responses", upstream.URL))
			request, recorder := chatRequestContext(fmt.Sprintf(`{"model":"grp","messages":[{"role":"user","content":"hi"}],"stream":%t,"stream_options":{"include_usage":true}}`, stream))
			s.chatCompletions(request)
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "OK") || strings.Contains(recorder.Body.String(), "tool_usage") || recorder.Header().Get("X-Elysia-Stream-Error") != "" {
				t.Fatalf("bad projected response: %d %s", recorder.Code, recorder.Body)
			}
			if !strings.Contains(recorder.Body.String(), `"service_tier":"default"`) || stream && !strings.Contains(recorder.Body.String(), "[DONE]") {
				t.Fatal("updated service tier or final stream marker missing", recorder.Body)
			}
			records := latestUsageRecords(t, s)
			if len(records) != 1 || records[0].Error != "" {
				t.Fatal(records)
			}
			body, exists, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil || !exists {
				t.Fatal(err)
			}
			var saved usageRecord
			if err = json.Unmarshal(body, &saved); err != nil {
				t.Fatal(err)
			}
			u := saved.ProtocolUsage
			if u == nil || u.Input.Count != 3 || u.Output.Count != 1 || u.Total.Count != 4 || len(u.Details) != 8 || u.Details["tools.image_generation.total_tokens"].Count != 12 || saved.BuiltinToolUsage.WebSearchCalls != 2 {
				t.Fatalf("original ledger lost or model totals contaminated: %+v %+v", u, saved.BuiltinToolUsage)
			}
			if string(u.Attribution.Bytes()) != attribution {
				t.Fatalf("attribution lost from persisted raw accounting: %s", u.Attribution.Bytes())
			}
			if string(u.CacheBilling.Bytes()) != billing || u.CacheRead.Count != 1 {
				t.Fatalf("disclosed billing replaced observed usage or was lost: %+v", u)
			}
			attributionIssue := false
			for _, issue := range saved.ConversionIssues {
				if strings.HasSuffix(issue.Path, "/usage/attribution") && issue.RuleID != "" && issue.PolicyHash != "" {
					attributionIssue = true
				}
			}
			if !attributionIssue {
				t.Fatal("attribution projection diagnostic not persisted")
			}
			n := 0
			for _, issue := range saved.ConversionIssues {
				if strings.Contains(issue.Path, "/details/tools.") && issue.RuleID != "" && issue.PolicyHash != "" {
					n++
				}
			}
			if n < 8 {
				t.Fatalf("missing persisted projection diagnostics: %+v", saved.ConversionIssues)
			}
		})
	}
}

func TestReportedToolCountSurvivesFramesWithoutUsage(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "openai-responses"))
	for _, count := range []int64{0, 2} {
		r := &usageRecord{}
		updateRecordProtocolUsage(r, &protocol.Usage{Details: map[string]protocol.Counter{"tools.web_search_calls": {Count: count, Origin: protocol.ObservedCount}}})
		if err := observeHostedTools(r, compiled, []byte(`{"type":"response.in_progress","response":{"id":"r"}}`)); err != nil {
			t.Fatal(err)
		}
		if r.BuiltinToolUsage.WebSearchCalls != int(count) {
			t.Fatal(r.BuiltinToolUsage)
		}
	}
}
