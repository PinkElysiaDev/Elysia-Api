package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestStreamIdleCancelsHeadersAndUsageTail(t *testing.T) {
	for _, hasHeaders := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "usage-tail"}[hasHeaders], func(t *testing.T) {
			closed := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				if hasHeaders {
					io.WriteString(w, "data: {\"finish_reason\":\"stop\"}\n\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer upstream.Close()
			transport := NewProtocolTransport(time.Second)
			operation := protocol.Operation{Method: "GET", Path: "/stream", Transport: protocol.SSE, Auth: protocol.Credential{Location: "none"}, Framing: &protocol.Framing{IdleMillis: 100}}
			response, err := transport.SendProtocolRequest(t.Context(), upstream.URL, "", operation, nil, nil)
			if response != nil {
				defer response.Body.Close()
				body, readErr := io.ReadAll(response.Body)
				err = readErr
				if !strings.Contains(string(body), "finish_reason") {
					t.Fatalf("lost observed frame: %s", body)
				}
			}
			if !errors.Is(err, errStreamIdle) {
				t.Fatalf("silence must fail, not EOF: %v", err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("idle request was not released")
			}
		})
	}
}

func TestStreamIdleExcludesConsumerTime(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, part := range []string{"a", "b", "c"} {
			io.WriteString(w, part)
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	transport := NewProtocolTransport(time.Second)
	response, err := transport.SendProtocolRequest(context.Background(), upstream.URL, "", protocol.Operation{Method: "GET", Path: "/", Transport: protocol.NDJSON, Auth: protocol.Credential{Location: "none"}, Framing: &protocol.Framing{IdleMillis: 100}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, 1)
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	rest, err := io.ReadAll(response.Body)
	if err != nil || string(first)+string(rest) != "abc" {
		t.Fatalf("consumer delay truncated stream: %s %s %v", first, rest, err)
	}
}
