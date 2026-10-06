package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An Anthropic upstream reports its cache-creation TTL breakdown, and the
// client speaks Chat. The bucket has no Chat representation, so the projection
// keeps the creation total, omits the bucket, and records a warning instead of
// failing a response the client can otherwise consume.
func TestCacheCreationBucketProjectionAcrossProtocols(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			usage := `{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":0}}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"r\",\"role\":\"assistant\",\"content\":[],\"usage\":%s}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\ndata: {\"type\":\"message_stop\"}\n\n", usage)
				} else {
					usage = strings.TrimSuffix(usage, "}") + `,"output_tokens":5}`
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"content":[{"type":"text","text":"ok"}],"usage":%s}`, usage)
				}
			}))
			defer upstream.Close()
			s := newTestServerWithStore(t, presetGroup(t, "custom:anthropic-api", upstream.URL))
			c, rec := chatRequestContext(fmt.Sprintf(`{"model":"grp","max_tokens":64,"stream":%v,"messages":[{"role":"user","content":"hello"}]}`, stream))
			s.chatCompletions(c)
			if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatalf("cross-protocol bucket must not fail: HTTP %d trailer=%q body=%s", rec.Code, rec.Result().Trailer.Get(gatewayStreamErrorTrailer), rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"cache_write_tokens":20`) {
				t.Fatalf("creation total lost: %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "ephemeral") {
				t.Fatalf("provider bucket leaked downstream: %s", rec.Body.String())
			}
			items := latestUsageRecords(t, s)
			if len(items) != 1 {
				t.Fatalf("usage records: %d", len(items))
			}
			data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			// The omission is a diagnostic, never a silent drop, and the record
			// still carries the complete provider counters from the upstream.
			found := false
			for _, issue := range record.ConversionIssues {
				if strings.Contains(issue.Path, "ephemeral") && issue.Severity == "warning" {
					found = true
				}
			}
			if !found {
				t.Fatalf("omission not recorded as a warning: %+v", record.ConversionIssues)
			}
			if record.ProtocolUsage == nil || record.ProtocolUsage.CacheCreation == nil || record.ProtocolUsage.CacheCreation.Count != 20 {
				t.Fatalf("semantic record lost the creation total: %+v", record.ProtocolUsage)
			}
			for _, name := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"} {
				if _, exists := record.ProtocolUsage.Details[name]; !exists {
					t.Fatalf("semantic record dropped the provider bucket %q", name)
				}
			}
		})
	}
}
