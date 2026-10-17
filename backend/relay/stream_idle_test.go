package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elysia-api/backend/protocol"
)

type streamRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn streamRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestStreamIdleLongThinking(t *testing.T) {
	for _, test := range []struct {
		name    string
		idle    int
		headers time.Duration
		gap     time.Duration
		parts   int
		phase   StreamIdlePhase
		elapsed time.Duration
	}{
		{name: "default survives slow headers", headers: 130 * time.Second, parts: 1, elapsed: 130 * time.Second},
		{name: "default survives long thinking and total duration", gap: 130 * time.Second, parts: 6, elapsed: 780 * time.Second},
		{name: "default still bounds headers", headers: 601 * time.Second, phase: StreamAwaitingResponse, elapsed: 600 * time.Second},
		{name: "default still bounds body", gap: 601 * time.Second, parts: 1, phase: StreamReadingBody, elapsed: 600 * time.Second},
		{name: "explicit longer wait", idle: 900000, gap: 601 * time.Second, parts: 1, elapsed: 601 * time.Second},
		{name: "explicit shorter wait", idle: 1000, gap: 2 * time.Second, parts: 1, phase: StreamReadingBody, elapsed: time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				transport := NewProtocolTransport(time.Second)
				transport.streamClient.Transport = streamRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					select {
					case <-time.After(test.headers):
					case <-request.Context().Done():
						return nil, context.Cause(request.Context())
					}
					reader, writer := io.Pipe()
					go func() {
						defer writer.Close()
						for range test.parts {
							select {
							case <-time.After(test.gap):
								if _, err := io.WriteString(writer, "data: {}\n\n"); err != nil {
									return
								}
							case <-request.Context().Done():
								writer.CloseWithError(context.Cause(request.Context()))
								return
							}
						}
					}()
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
				})
				operation := protocol.Operation{Method: "GET", Path: "/", Transport: protocol.SSE, Auth: protocol.Credential{Location: "none"}}
				if test.idle != 0 {
					operation.Framing = &protocol.Framing{IdleMillis: test.idle}
				}
				response, err := transport.SendProtocolRequest(t.Context(), "https://upstream.test", "", operation, nil, nil)
				var body []byte
				if response != nil {
					defer response.Body.Close()
					body, err = io.ReadAll(response.Body)
				}
				if elapsed := time.Since(start); elapsed != test.elapsed {
					t.Fatalf("want elapsed %s, got %s", test.elapsed, elapsed)
				}
				if test.phase != "" {
					var idle *StreamIdleError
					if !errors.As(err, &idle) || idle.Phase != test.phase {
						t.Fatalf("want timeout in %s, got %v", test.phase, err)
					}
				} else if err != nil || string(body) != strings.Repeat("data: {}\n\n", test.parts) {
					t.Fatalf("long thinking interrupted: %q, %v", body, err)
				}
			})
		})
	}
}

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
			var idle *StreamIdleError
			wantPhase := StreamAwaitingResponse
			if hasHeaders {
				wantPhase = StreamReadingBody
			}
			if !errors.As(err, &idle) || idle.Phase != wantPhase || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("idle attribution/status semantics changed: %v", err)
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
