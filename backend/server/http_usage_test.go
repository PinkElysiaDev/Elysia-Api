package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayFinalizeFailureSettlesOnce(t *testing.T) {
	for _, shouldFail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "finalize-error"}[shouldFail], func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			events := make(requestLogEvents, 16)
			server.logger = logging.New(logging.Options{Level: "debug", Format: "json"}, nil)
			server.logger.SetOutput(events)
			server.engine.Use(server.requestLifecycle(), server.requestLogging(), func(ctx context.Context, c *app.RequestContext) {
				c.Next(ctx)
				if shouldFail {
					stream := currentResponseStream(c)
					if stream == nil {
						t.Error("fixture did not start a stream")
						return
					}
					stream.ExtWriter = &finalizeFailureWriter{ExtWriter: stream.ExtWriter, err: errors.New("synthetic final chunk failure")}
				}
			})
			activateDiscoveryPresets(t, server)
			service, _ := server.protocolService()
			upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, openAIChunk("c1", map[string]any{"role": "assistant", "content": "synthetic complete response"}, "", nil))
				_, _ = io.WriteString(w, openAIChunk("c1", map[string]any{}, "stop", map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5})+openAIDone())
			}))
			defer provider.Close()
			setupGatewayModel(t, server, upstream, provider.URL)
			web := startTestServer(server.engine)
			defer web.Close()
			request, _ := http.NewRequest("POST", web.URL+"/gateway/"+protocol.PresetChatCompletionsID+"/chat/completions", strings.NewReader(`{"model":"group","stream":true,"messages":[{"role":"user","content":"synthetic"}]}`))
			request.Header.Set("Authorization", "Bearer gateway-test-token")
			request.Header.Set("Content-Type", "application/json")
			response, err := web.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || !strings.Contains(string(body), "synthetic complete response") || (readErr != nil) != shouldFail {
				t.Fatal("unexpected wire outcome", response.StatusCode, readErr)
			}
			var completion map[string]any
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			for completion["message"] != "request completed" {
				select {
				case event := <-events:
					if err := sonic.ConfigStd.Unmarshal(event, &completion); err != nil {
						t.Fatal(err)
					}
				case <-deadline.C:
					t.Fatal("missing completion log")
				}
			}
			_, records, err := server.store.QueryUsageLogs(t.Context(), storage.UsageQuery{})
			if err != nil || len(records) != 1 || calls.Load() != 1 {
				t.Fatal("duplicate or missing settlement", len(records), calls.Load(), err)
			}
			var record usageRecord
			if err := sonic.ConfigStd.Unmarshal([]byte(storedRecordJSON(t, server.store, records[0].RequestID)), &record); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantOutcome := 200, deliveryCompleted
			if shouldFail {
				wantStatus, wantOutcome = 502, deliveryFailed
			}
			if record.StatusCode != wantStatus || record.Delivery == nil || record.Delivery.HTTPStatus != 200 || record.Delivery.Outcome != wantOutcome || records[0].TotalTokens != 5 {
				t.Fatalf("logical and wire results diverged: status=%d delivery=%+v tokens=%d", record.StatusCode, record.Delivery, records[0].TotalTokens)
			}
			if completion["status_code"] != float64(200) {
				t.Fatal("completion rewrote the committed wire status", completion)
			}
			if shouldFail && (record.Delivery.Phase != deliveryFinalize || record.ErrorKind != deliveryTransport || record.Error == "") {
				t.Fatal(record.Delivery, record.ErrorKind)
			}
		})
	}
}

func TestHTTPUsageResetKeepsOriginalSettlementGeneration(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	request := newTestContext(httptest.NewRecorder())
	record := server.initUsageRecord(t.Context(), request, time.Now(), nil, "")
	server.recordHTTPUsage(request, record)
	reset := newTestContext(httptest.NewRecorder())
	server.resetUsage(t.Context(), reset)
	flushTestResponse(reset)
	finishHTTPRequest(request)
	if count, err := server.store.CountUsageRecords(t.Context()); err != nil || count != 0 {
		t.Fatal("completion resurrected a pre-reset record", count, err)
	}
	next := newTestContext(httptest.NewRecorder())
	server.recordHTTPUsage(next, server.initUsageRecord(t.Context(), next, time.Now(), nil, ""))
	finishHTTPRequest(next)
	if count, err := server.store.CountUsageRecords(t.Context()); err != nil || count != 1 {
		t.Fatal("post-reset settlement was dropped", count, err)
	}
}

func TestHTTPUsageReleasesCaptureAndKeepsPrimary(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	request := newTestContext(httptest.NewRecorder())
	record := server.initUsageRecord(t.Context(), request, time.Now(), nil, "")
	record.bodyOpts.maxBytes = 64
	capture := installDownstreamCapture(request, record, 2<<20)
	writer := startStream(request)
	_, _ = writer.Write([]byte(strings.Repeat("x", 2<<20)))
	record.Error, record.ErrorKind, record.StatusCode, record.UpstreamHTTPStatus = "synthetic provider failure", "rate_limit", 429, 429
	server.recordHTTPUsage(request, record)
	if record.downstream != nil || capture.buf != nil || capture.response != nil || currentResponseStream(request).capture != nil || record.assets.byHash != nil || record.assets.memo != nil {
		t.Fatal("pending completion retained capture-only state")
	}
	currentResponseStream(request).err = wrapDownstreamError(errors.New("synthetic finalization failure"), deliveryFinalize)
	finishHTTPRequest(request)
	if record.StatusCode != 429 || record.ErrorKind != "rate_limit" || record.Error != "synthetic provider failure" || record.Delivery.Outcome != deliveryFailed {
		t.Fatal("delivery replaced the provider's primary failure", record.StatusCode, record.ErrorKind)
	}
}

func TestHertzDeduplicatesOnlyReportedTypedDeliveryErrors(t *testing.T) {
	for _, handler := range []network.HandleSpecificError{&deliveryConn{&standard.Conn{}}, &deliveryTLSConn{&standard.TLSConn{}}} {
		failure := wrapDownstreamError(errors.New("synthetic socket failure"), deliveryFlush).(*downstreamWriteError)
		if handler.HandleSpecificError(failure, "") {
			t.Fatal("unreported error was hidden")
		}
		failure.isReported = true
		if !handler.HandleSpecificError(failure, "") {
			t.Fatal("reported error was emitted twice")
		}
		if handler.HandleSpecificError(errors.New(failure.Error()), "") {
			t.Fatal("an unrelated equal message was hidden")
		}
	}
	unknown := &testConnection{}
	if observeHTTPConnection(unknown) != unknown {
		t.Fatal("adapter changed an unknown transport")
	}
}

func TestHertzDeliveryLogHasOneOwner(t *testing.T) {
	for _, hasRequestLogger := range []bool{false, true} {
		t.Run(map[bool]string{false: "framework-only", true: "request-owner"}[hasRequestLogger], func(t *testing.T) {
			events := make(requestLogEvents, 32)
			logger := logging.New(logging.Options{Level: "debug", Format: "json"}, nil)
			logger.SetOutput(events)
			previous := logging.Default()
			logging.SetDefault(logger)
			t.Cleanup(func() { logging.SetDefault(previous) })
			server := &Server{logger: logger}
			engine := newTestEngine(t)
			if hasRequestLogger {
				engine.Use(server.requestLogging())
			}
			engine.GET("/stream", func(_ context.Context, c *app.RequestContext) {
				stream := startStream(c)
				_, _ = stream.Write([]byte("synthetic complete response"))
				_ = stream.Flush()
				owner := currentResponseStream(c)
				owner.ExtWriter = &finalizeFailureWriter{ExtWriter: owner.ExtWriter, err: errors.New("synthetic final chunk failure")}
			})
			web := startTestServer(engine)
			response, err := web.Client().Get(web.URL + "/stream")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			web.Close()
			requestLogs, frameworkLogs := 0, 0
			for len(events) > 0 {
				var event map[string]any
				if err := sonic.ConfigStd.Unmarshal(<-events, &event); err != nil {
					t.Fatal(err)
				}
				message, _ := event["message"].(string)
				if message == "request completed" {
					requestLogs++
				}
				if event["component"] == "hertz" && strings.Contains(message, "synthetic final chunk failure") {
					frameworkLogs++
				}
			}
			if hasRequestLogger && (requestLogs != 1 || frameworkLogs != 0) || !hasRequestLogger && (requestLogs != 0 || frameworkLogs != 1) {
				t.Fatal("delivery logging was duplicated or lost", requestLogs, frameworkLogs)
			}
		})
	}
}
