package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamUsageTailZeroReachesClientAndStorage(t *testing.T) {
	for _, platform := range []string{"anthropic", "custom:anthropic-api", "custom:usage-tail-copy"} {
		t.Run(platform, func(t *testing.T) {
			preset := presetDefinition(t, "anthropic-api")
			preset.ID = "usage-tail-copy"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":90,\"cache_creation_input_tokens\":20}}}\n\n"+
					"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"+
					"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
					"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"+
					"data: {\"type\":\"message_delta\",\"usage\":{\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0,\"output_tokens\":0}}\n\n"+
					"data: {\"type\":\"message_stop\"}\n\n")
			}))
			defer upstream.Close()
			s := newTestServerWithStore(t, presetGroup(t, platform, upstream.URL), preset)
			context, response := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			s.chatCompletions(context)
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"error"`) {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"cached_tokens":0`) {
				t.Fatalf("zero cache omitted: %s", response.Body.String())
			}
			logs := latestUsageRecords(t, s)
			payload, _, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := json.Unmarshal(payload, &record); err != nil {
				t.Fatal(err)
			}
			for name, count := range map[string]*int{"cache": record.Usage.CacheHitTokens, "creation": record.UsageDetail.CacheCreationInputTokens, "output": record.Usage.OutputTokens} {
				if count == nil || *count != 0 {
					t.Fatalf("%s missing or nonzero: %+v", name, record.UsageDetail)
				}
			}
			if got := fmt.Sprint(derefInt(record.Usage.InputTokens), "/", derefInt(record.Usage.TotalTokens)); got != "10/10" {
				t.Fatalf("normalized tail: %s, %s", got, payload)
			}
		})
	}
}
