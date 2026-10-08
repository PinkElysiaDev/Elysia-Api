package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func TestCustomProtocolStreamTrailingUsageAfterFinishEndToEnd(t *testing.T) {
	definition := cumulativeWireDefinition(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"payload\":{\"text\":\"hello\"},\"finished\":true}\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"usage\":{\"input_tokens\":11,\"output_tokens\":22}}\n\ndata: END\n\n")
	}))
	defer upstream.Close()
	s := newTestServer(t, standaloneGroup(t, definition, upstream.URL), definition)
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	for _, want := range []string{`"content":"hello"`, `"prompt_tokens":11`, `"completion_tokens":22`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("trailing usage lost %s: %s", want, rec.Body)
		}
	}
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].TotalTokens != 33 {
		t.Fatal(logs)
	}
}

// A semantic finish is not a transport end. Cancellation must close the
// hanging upstream and retain observed usage without fabricating success.
func TestCustomProtocolStreamHangingAfterFinishCanBeCanceled(t *testing.T) {
	definition := cumulativeWireDefinition(t)
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"payload\":{\"text\":\"done\"},\"finished\":true,\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	s := newTestServer(t, standaloneGroup(t, definition, upstream.URL), definition)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/gateway/vendor-cumulative/v2/generate/grp", strings.NewReader(`{"deployment":"grp","turns":[{"actor":"user","segments":[{"text":"hi"}]}],"parameters":{"stream":true}}`))
	c.Params = gin.Params{{Key: "protocolId", Value: definition.ID}, {Key: "path", Value: "/v2/generate/grp"}}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &cancelAfterFlushWriter{c.Writer, rec, `"count":1`, cancel, ctx}
	s.gatewayProtocol(c)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled upstream was not released")
	}
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != 499 || logs[0].TotalTokens != 3 || strings.Contains(rec.Body.String(), "data: END") {
		t.Fatal("cancellation disguised as success or usage lost", logs, rec.Body)
	}
}

func TestCustomProtocolStreamEmptyCompletionWithFinishReasonEndToEnd(t *testing.T) {
	definition := standaloneWireDefinition(t, protocol.SSE)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"event\":{\"type\":\"response.finished\",\"response\":{\"schemaVersion\":1,\"content\":[],\"attributes\":{\"finishReason\":\"content_filter\"}}}}\n\n")
	}))
	defer upstream.Close()
	s := newTestServer(t, standaloneGroup(t, definition, upstream.URL), definition)
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	logs := latestUsageRecords(t, s)
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || !strings.Contains(rec.Body.String(), `"finish_reason":"content_filter"`) || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatal(logs, rec.Body)
	}
}

func TestCustomProtocolStreamRetryableFailureRecordsUsageOnce(t *testing.T) {
	definition := standaloneWireDefinition(t, protocol.SSE)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer upstream.Close()
	groups := standaloneGroup(t, definition, upstream.URL)
	backup := groups[0].Models[0]
	backup.ID = "backup"
	groups[0].Models = append(groups[0].Models, backup)
	groups[0].MaxRetries = 1
	s := newTestServer(t, groups, definition)
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	logs := latestUsageRecords(t, s)
	if rec.Code != http.StatusInternalServerError || attempts.Load() != 2 || len(logs) != 1 || logs[0].StatusCode != http.StatusInternalServerError {
		t.Fatal(rec.Code, attempts.Load(), logs, rec.Body)
	}
}

// An empty legacy nested mapping requires author repair. Import must not
// activate an implicit inheritance rule or the retired stream executor.
func TestLegacyEmptyStreamMappingRequiresExplicitRepair(t *testing.T) {
	definition, issues := relay.ImportLegacyProtocol([]byte(`{"id":"designer-trap","request":{"path":"/chat","body":{"model":{"field":"model"}}},"response":{"textPath":"text","stream":{"response":{"body":{}}}}}`), protocol.CapabilitySet{protocol.TextCapability: true})
	if definition == nil || protocol.IssuesError(issues) == nil {
		t.Fatal("unverified stream import accepted", issues)
	}
	for _, issue := range issues {
		if issue.Path == "/response/stream" {
			return
		}
	}
	t.Fatal("stream repair diagnostic missing", issues)
}
