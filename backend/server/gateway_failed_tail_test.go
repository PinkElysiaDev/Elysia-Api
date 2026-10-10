package server

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderFailureRetainsCauseWhenUsageTailIsUnsupported(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"id":"r","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"error":{"type":"server_error","message":"synthetic provider overloaded"}}`,
			`{"id":"r","model":"m","created":1,"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3,"vendor_tail":42}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
	}))
	defer upstream.Close()
	groups := presetGroup(t, "custom:openai-chat-completions", upstream.URL)
	groups[0].MaxRetries = 3
	s := newTestServer(t, groups)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"grp","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	s.chatCompletions(c)
	body := recorder.Body.String()
	if calls.Load() != 1 || strings.Count(body, `"type":"error"`) != 1 || !strings.Contains(body, "synthetic provider overloaded") || recorder.Header().Get(gatewayStreamErrorTrailer) == "" {
		t.Fatal(calls.Load(), body, recorder.Header())
	}
	records := latestUsageRecords(t, s)
	if len(records) != 1 || records[0].StatusCode < 400 || !strings.Contains(records[0].Error, "synthetic provider overloaded") || !strings.Contains(records[0].Error, "unsupported_native") || strings.Contains(records[0].Error, "downstream error frame") {
		t.Fatal(records)
	}
}
