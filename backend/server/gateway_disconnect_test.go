//go:build windows

package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayWindowsDisconnectSettlesCanceledCall(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			s.engine.Use(s.requestLifecycle())
			activateDiscoveryPresets(t, s)
			service, err := s.protocolService()
			if err != nil {
				t.Fatal(err)
			}
			upstream, ok := service.Pin(protocol.PresetChatCompletionsID)
			if !ok {
				t.Fatal("Chat preset was not activated")
			}
			started := make(chan error, 1)
			canceled := make(chan error, 1)
			release := make(chan struct{})
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					started <- err
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					// A complete, valid Chat frame reaches the client before it
					// closes. Missing termination is a consequence of cancellation.
					_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-cancel\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"started\"},\"finish_reason\":null}]}\n\n")
					w.(http.Flusher).Flush()
				}
				started <- nil
				select {
				case <-r.Context().Done():
				case <-release:
				}
				canceled <- r.Context().Err()
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			web := startTestServer(s.engine)
			defer web.Close()
			defer close(release)
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(web.URL, "http://"), disconnectIntegrationTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(disconnectIntegrationTimeout)); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"model":"group","stream":%t,"messages":[{"role":"user","content":"synthetic cancellation"}]}`, stream)
			if _, err := fmt.Fprintf(conn, "POST /gateway/%s/chat/completions HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer gateway-test-token\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", protocol.PresetChatCompletionsID, len(body), body); err != nil {
				t.Fatal(err)
			}
			if err := awaitDisconnectResult(t, started, "gateway upstream start"); err != nil {
				t.Fatal(err)
			}
			if stream {
				response, err := http.ReadResponse(bufio.NewReader(conn), nil)
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK {
					t.Fatal("stream did not start", response.StatusCode)
				}
				reader := bufio.NewReader(response.Body)
				var frame strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Fatal(err)
					}
					frame.WriteString(line)
					if line == "\n" || line == "\r\n" {
						break
					}
				}
				if !strings.Contains(frame.String(), `"content":"started"`) {
					t.Fatal("client did not consume the first generation frame")
				}
			}
			s.httpRequests.mu.Lock()
			active := len(s.httpRequests.active)
			s.httpRequests.mu.Unlock()
			if active != 1 {
				t.Fatal("gateway request was not held active", active)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if err := awaitDisconnectResult(t, canceled, "gateway upstream cancellation"); !errors.Is(err, context.Canceled) {
				t.Fatal("client disconnect did not cancel upstream", err)
			}

			deadline := time.Now().Add(disconnectIntegrationTimeout)
			for {
				_, records, err := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{})
				if err != nil {
					t.Fatal(err)
				}
				preparation := s.preparationLimiter().Snapshot()
				s.httpRequests.mu.Lock()
				active = len(s.httpRequests.active)
				s.httpRequests.mu.Unlock()
				if len(records) > 1 || calls.Load() > 1 {
					t.Fatal("cancellation duplicated generation or settlement", calls.Load(), len(records))
				}
				if len(records) == 1 && active == 0 && preparation.Active == 0 && preparation.Queued == 0 && preparation.ReservedBodyBytes == 0 {
					var record usageRecord
					if err := sonic.ConfigStd.Unmarshal([]byte(storedRecordJSON(t, s.store, records[0].RequestID)), &record); err != nil {
						t.Fatal(err)
					}
					if record.StatusCode != statusClientClosedRequest || record.ErrorKind != ErrorKindClientCanceled || record.RetryCount != 0 || len(record.RetryEvents) != 0 || calls.Load() != 1 {
						t.Fatalf("wrong cancellation settlement: status=%d kind=%s retries=%d events=%d calls=%d", record.StatusCode, record.ErrorKind, record.RetryCount, len(record.RetryEvents), calls.Load())
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("canceled request did not release ownership: records=%d active=%d preparation=%+v", len(records), active, preparation)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
