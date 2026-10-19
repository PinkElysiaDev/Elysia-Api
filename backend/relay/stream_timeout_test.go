package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// Delay the first byte past the ordinary client timeout. The streaming client
// must still read all frames; no per-frame sleeps are needed to prove this.
func TestOpenAIStreamNotCutByClientTimeout(t *testing.T) {
	t.Parallel()
	const clientTimeout = 50 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		time.Sleep(2 * clientTimeout)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"chunk\":%d}\n\n", i)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	a := NewProtocolTransport(clientTimeout)
	resp, err := a.SendProtocolRequest(t.Context(), srv.URL, "k", protocol.Operation{Method: "POST", Path: "/generate", Transport: protocol.SSE, Auth: protocol.Credential{Location: "none"}}, []byte(`{"stream":true}`), nil)
	if err != nil {
		t.Fatalf("stream request should not be cut by client timeout, got: %v", err)
	}
	defer resp.Body.Close()

	// Read through the terminal frame even after the ordinary timeout elapsed.
	scanner := bufio.NewScanner(resp.Body)
	var lines int
	gotDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "data:") {
			lines++
		}
		if strings.Contains(line, "[DONE]") {
			gotDone = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("stream read interrupted (timeout cut the connection?): %v", err)
	}
	if !gotDone || lines < 4 {
		t.Fatalf("expected full stream incl [DONE], got %d data lines, done=%v", lines, gotDone)
	}
}

// The ordinary client must cancel a silent upstream when its timeout expires.
func TestOpenAINonStreamStillRespectsTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	a := NewProtocolTransport(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	resp, err := a.SendProtocolRequest(ctx, srv.URL, "k", protocol.Operation{Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}}, []byte(`{}`), nil)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("expected client timeout before outer deadline, got %v (context: %v)", err, ctx.Err())
	}
}
