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
	"testing/synctest"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/admission"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

type preparationBody struct {
	io.Reader
	bytes int
}

func (r *preparationBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += n
	return n, err
}

func (*preparationBody) Close() error { return nil }

func preparationProtocol(t *testing.T) *protocol.Compiled {
	t.Helper()
	compiler, _ := protocol.NewCompiler(protocol.DefaultLimits(), builtin.Modules(), nil)
	definitions, _ := builtin.Definitions()
	for _, raw := range definitions {
		entry, issues := compiler.Compile(raw.Bytes())
		if entry == nil {
			t.Fatal(issues)
		}
		if entry.Identity().DefinitionID == protocol.PresetAnthropicID {
			return entry
		}
	}
	t.Fatal("missing Anthropic codec")
	return nil
}

// Authentication remains before admission/body reading.
func TestPreparationRejectsBeforeBodyRead(t *testing.T) {
	zero := 0
	cfg := &config.Config{MaxBodyBytes: 64}
	cfg.SetPreparation(config.PreparationConfig{MaxConcurrent: 1, MaxQueued: &zero, MaxReservedBodyBytes: 64})
	s := &Server{config: cfg}
	compiled := preparationProtocol(t)
	hold, _ := s.preparationLimiter().Acquire(t.Context(), 64)
	ctx, recorder := adminProtocolContext("POST", "/v1/messages", `{}`)
	body := &preparationBody{Reader: strings.NewReader(`{}`)}
	ctx.Request.SetBodyStream(body, -1)
	_, ok := s.readGatewayRequest(contextForTest(ctx), ctx, compiled)
	flushTestResponse(ctx)
	if ok || recorder.Code != 503 || recorder.Header().Get("Retry-After") != "1" || body.bytes != 0 {
		t.Fatalf("overload read body: %d %d", recorder.Code, body.bytes)
	}
	hold.Release()
	if stats := s.preparationLimiter().Snapshot(); stats.Active != 0 || stats.ReservedBodyBytes != 0 || stats.Queued != 0 {
		t.Fatal(stats)
	}
	engine := newTestEngine(t)
	engine.POST("/v1/messages", s.authMiddleware(), func(ctx context.Context, c *app.RequestContext) {
		t.Error("unauthenticated request entered preparation")
	})
	body = &preparationBody{Reader: strings.NewReader(`{}`)}
	req := httptest.NewRequest("POST", "/v1/messages", body)
	rec := httptest.NewRecorder()
	serveTestHTTP(engine, rec, req)
	if rec.Code != 401 || body.bytes != 0 {
		t.Fatalf("auth/body boundary: %d %d", rec.Code, body.bytes)
	}
}

func TestPreparationDeclaredAndUnknownLength(t *testing.T) {
	compiled := preparationProtocol(t)
	for _, tc := range []struct {
		name     string
		declared int64
		body     string
		status   int
	}{{"known", 2, `{}`, 200}, {"chunked", -1, `{}`, 200}, {"underdeclared", 1, strings.Repeat("x", 4096), 400}, {"overdeclared", 3, `{}`, 400}, {"unknown over budget", -1, strings.Repeat("x", 65), 413}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{MaxBodyBytes: 64}
			cfg.SetPreparation(config.PreparationConfig{MaxConcurrent: 1, MaxReservedBodyBytes: 64})
			s := &Server{config: cfg}
			ctx, rec := adminProtocolContext("POST", "/v1/messages", "")
			body := &preparationBody{Reader: strings.NewReader(tc.body)}
			ctx.Request.SetBodyStream(body, int(tc.declared))
			_, ok := s.readGatewayRequest(contextForTest(ctx), ctx, compiled)
			flushTestResponse(ctx)
			if ok != (tc.status == 200) || !ok && rec.Code != tc.status {
				t.Fatalf("status=%d ok=%t", rec.Code, ok)
			}
			if tc.declared == 1 && body.bytes > 2 {
				t.Fatal("untrusted length extended reservation", body.bytes)
			}
			if ok {
				stats := s.preparationLimiter().Snapshot()
				want := tc.declared
				if want < 0 {
					want = 64
				}
				if stats.Active != 1 || stats.ReservedBodyBytes != want {
					t.Fatal(stats)
				}
			}
			s.releaseGatewayPreparation(ctx)
			if stats := s.preparationLimiter().Snapshot(); stats.Active != 0 || stats.ReservedBodyBytes != 0 {
				t.Fatal(stats)
			}
		})
	}
}

func TestPreparationStartupPolicyAndClose(t *testing.T) {
	zero := 0
	cfg := &config.Config{}
	cfg.SetPreparation(config.PreparationConfig{MaxConcurrent: 1, MaxQueued: &zero})
	s := &Server{config: cfg}
	l := s.preparationLimiter()
	cfg.SetPreparation(config.PreparationConfig{MaxConcurrent: 2})
	lease, _ := l.Acquire(t.Context(), 1)
	if _, err := l.Acquire(t.Context(), 1); !errors.Is(err, admission.ErrFull) {
		t.Fatal("startup policy changed without restart", err)
	}
	lease.Release()
	l.Close()
	if _, err := l.Acquire(t.Context(), 1); !errors.Is(err, admission.ErrClosed) {
		t.Fatal("closed server admitted work", err)
	}
}

func TestPreparationQueuedCancellationAndShutdown(t *testing.T) {
	compiled := preparationProtocol(t)
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				one := 1
				cfg := &config.Config{MaxBodyBytes: 64}
				cfg.SetPreparation(config.PreparationConfig{MaxConcurrent: 1, MaxQueued: &one, MaxReservedBodyBytes: 64})
				s := &Server{config: cfg}
				hold, _ := s.preparationLimiter().Acquire(t.Context(), 64)
				ctx, rec := adminProtocolContext("POST", "/v1/messages", `{}`)
				requestContext, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctx.Set("testContext", requestContext)
				body := &preparationBody{Reader: strings.NewReader(`{}`)}
				ctx.Request.SetBodyStream(body, -1)
				done := make(chan bool, 1)
				go func() {
					_, ok := s.readGatewayRequest(contextForTest(ctx), ctx, compiled)
					flushTestResponse(ctx)
					done <- ok
				}()
				synctest.Wait()
				if s.preparationLimiter().Snapshot().Queued != 1 {
					t.Fatal("request did not queue before body read")
				}
				want := 503
				if mode == "cancel" {
					want = 499
					cancel()
				} else {
					s.preparationLimiter().Close()
				}
				if <-done || rec.Code != want || body.bytes != 0 {
					t.Fatalf("queued request consumed body: %d %d", rec.Code, body.bytes)
				}
				hold.Release()
				if stats := s.preparationLimiter().Snapshot(); stats.Active != 0 || stats.Queued != 0 || stats.ReservedBodyBytes != 0 {
					t.Fatal(stats)
				}
			})
		})
	}
}

func TestPreparationReleasedBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	started, unblock := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(started)
			<-unblock
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"complete"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`)
	}))
	defer provider.Close()
	s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: provider.URL, Platform: "claude", APIKey: "synthetic"}}}})
	zero := 0
	s.config.SetPreparation(config.PreparationConfig{MaxConcurrent: 1, MaxQueued: &zero})
	s.engine.POST("/v1/messages", s.chatCompletions)
	gateway := startTestServer(s.engine)
	defer gateway.Close()
	client := gateway.Client()
	client.Timeout = 10 * time.Second
	do := func() error {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", gateway.URL+"/v1/messages", strings.NewReader(`{"model":"grp","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, response.Body)
		if response.StatusCode != 200 {
			return errors.New(response.Status)
		}
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- do()
	}()
	select {
	case <-started:
	case err := <-done:
		close(unblock)
		t.Fatal("first request did not reach provider", err)
	}
	if stats := s.preparationLimiter().Snapshot(); stats.Active != 0 {
		close(unblock)
		t.Fatal("lease spans provider I/O", stats)
	}
	err := do()
	close(unblock)
	if first := <-done; err != nil || first != nil {
		t.Fatal(err, first)
	}
	if stats := s.preparationLimiter().Snapshot(); stats.Active != 0 || stats.Queued != 0 || stats.ReservedBodyBytes != 0 || stats.PeakActive != 1 || calls.Load() != 2 {
		t.Fatal(stats, calls.Load())
	}
}
