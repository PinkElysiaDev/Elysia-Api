package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/storage"
)

func TestCatalogShutdownCancelsAndWaitsForFetch(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer upstream.Close()
	catalog := newModelCatalog(func() config.ModelCatalogConfig { return config.ModelCatalogConfig{URL: upstream.URL} }, nil)
	go catalog.runPeriodic()
	if !catalog.triggerRefreshIfNeeded(true) {
		t.Fatal("catalog refresh did not start")
	}
	<-started
	done := make(chan struct{})
	go func() { catalog.shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("catalog shutdown did not cancel its active HTTP fetch")
	}
	select {
	case <-canceled:
	case <-time.After(500 * time.Millisecond):
		t.Error("catalog shutdown returned while the HTTP fetch was still active")
	}
	close(release)
	<-done
	if catalog.triggerRefreshIfNeeded(true) {
		t.Error("closed catalog accepted a new fetch")
	}
}

type shutdownStreamCaller struct {
	server  *Server
	started chan struct{}
	release chan struct{}
}

func (c *shutdownStreamCaller) Call(ctx context.Context, _ agent.CallRequest, _ agent.StreamCallbacks) (*agent.CallResult, error) {
	close(c.started)
	select {
	case <-ctx.Done():
	case <-c.release:
	}
	c.server.recordUsage(&usageRecord{RequestID: "last-agent-call", StartedAt: time.Now(), EndedAt: time.Now(), StatusCode: 499})
	c.server.logSystemEvent("info", "agent call canceled during shutdown", map[string]any{"request_id": "last-agent-call"})
	return &agent.CallResult{Text: "partial response"}, ctx.Err()
}

func TestShutdownStopsDetachedAgentThenFlushesAndClosesStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shutdown.sqlite3")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Server{store: store, config: &config.Config{}}
	s.logger = logging.New(logging.Options{Level: "error", PersistLevel: "error", Format: "json"}, logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	s.startUsageWriter()
	session, err := store.CreateAgentSession(context.Background(), storage.AgentSessionUpsert{Settings: agent.Settings{ModelName: "slow"}})
	if err != nil {
		t.Fatal(err)
	}
	caller := &shutdownStreamCaller{server: s, started: make(chan struct{}), release: make(chan struct{})}
	registry, err := agent.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	s.agentEngineInst = agent.NewEngine(caller, store, registry, nil, nil, agent.Options{EventBuffer: 1})
	events, err := s.agentEngineInst.RunTurn(context.Background(), session.ID, &agent.UserContent{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	<-caller.started
	// A disconnected browser leaves the event buffer full. Shutdown must still drain.
	done := make(chan struct{})
	go func() { s.doShutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("shutdown did not stop the detached Agent turn")
	}
	close(caller.release)
	<-done
	if s.agentEngineInst.IsRunning(session.ID) {
		t.Fatal("agent still owns storage after server shutdown")
	}
	if _, err := s.agentEngineInst.RunTurn(context.Background(), session.ID, nil); !errors.Is(err, agent.ErrEngineClosed) {
		t.Errorf("stopped Agent engine accepted another turn: %v", err)
	}
	for range events {
	}
	if err := store.Ping(context.Background()); err == nil {
		t.Error("server shutdown did not close storage")
	}
	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	finished, err := reopened.GetSession(context.Background(), session.ID)
	if err != nil || finished.Status != agent.StatusIdle {
		t.Fatalf("agent terminal status must be persisted before close: session=%+v err=%v", finished, err)
	}
	if _, found, err := reopened.GetUsageRecordJSON(context.Background(), "last-agent-call"); err != nil || !found {
		t.Fatalf("last usage record must be flushed before close: found=%v err=%v", found, err)
	}
	_, logs, err := reopened.QuerySystemLogs(t.Context(), 10, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range logs {
		if event.Message == "agent call canceled during shutdown" {
			found = true
		}
	}
	if !found || s.shutdownErr != nil {
		t.Fatalf("final system event not durable: logs=%+v shutdown=%v", logs, s.shutdownErr)
	}
}

func TestShutdownCancelsRunningAndQueuedSourceRefreshes(t *testing.T) {
	started := make(chan struct{}, sourceRefreshConcurrency)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	for i := 0; i <= sourceRefreshConcurrency; i++ {
		source := storage.ModelSource{ID: string(rune('a' + i)), BaseURL: upstream.URL, Platform: "custom:openai-chat-completions", AutoFetchModels: true}
		if err := s.store.UpsertSource(t.Context(), source); err != nil {
			t.Fatal(err)
		}
		if !s.launchSourceRefresh(source.ID) {
			t.Fatal("source refresh was rejected before shutdown")
		}
	}
	for i := 0; i < sourceRefreshConcurrency; i++ {
		<-started
	}
	done := make(chan struct{})
	go func() { s.doShutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("source refresh work did not stop before storage close")
	}
	close(release)
	<-done
	if calls.Load() != sourceRefreshConcurrency {
		t.Error("queued source refresh contacted its upstream after shutdown")
	}
	if s.launchSourceRefresh("late") {
		t.Error("server accepted a source refresh after shutdown")
	}
}

func TestShutdownCancelsActiveHTTPHandlerBeforeClosingStore(t *testing.T) {
	s := newKeyPermissionTestServer(t)
	s.initLifecycle()
	s.startUsageWriter()
	s.engine = newTestEngine(t)
	s.engine.Use(s.requestLifecycle())
	started, handled := make(chan struct{}), make(chan struct{})
	s.engine.GET("/stream", func(ctx context.Context, c *app.RequestContext) {
		close(started)
		<-ctx.Done()
		s.recordUsage(&usageRecord{RequestID: "last-http-call", StartedAt: time.Now(), EndedAt: time.Now(), StatusCode: 499})
		close(handled)
	})
	web := startTestServer(s.engine)
	defer web.Close()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		if resp, err := http.Get(web.URL + "/stream"); err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	s.doShutdown()
	select {
	case <-handled:
	default:
		t.Fatal("shutdown returned before the active handler released storage")
	}
	<-clientDone
	if err := s.store.Ping(context.Background()); err == nil {
		t.Error("storage remained open after all handlers exited")
	}
}

func TestRequestedShutdownBeforeListenClosesAllResources(t *testing.T) {
	no := false
	cfg := &config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "early-stop.sqlite3"),
		Host:               "127.0.0.1",
		OpenBrowserOnStart: &no,
		ModelCatalog:       config.ModelCatalogConfig{Enabled: &no},
	}
	s := New(cfg)
	s.RequestShutdown()
	if err := s.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Ping(context.Background()); err == nil {
		t.Error("early shutdown left storage open")
	}
}

func TestStartupFailureDrainsStartedServices(t *testing.T) {
	for _, stage := range []string{"hook", "jobs", "bind"} {
		t.Run(stage, func(t *testing.T) {
			// Keep the address occupied: if startup hooks succeed, binding fails.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cfg := startupFixtureConfig(t, t.TempDir())
			cfg.Host, cfg.Port = "127.0.0.1", listener.Addr().(*net.TCPAddr).Port
			s := New(cfg)
			if s.startupErr != nil {
				t.Fatal(s.startupErr)
			}
			failure := errors.New("startup hook failed")
			switch stage {
			case "hook":
				s.engine.OnRun = append(s.engine.OnRun, func(context.Context) error { return failure })
			case "jobs":
				s.gatewayJobs.stop()
			}
			err = s.ListenAndServe()
			if err == nil {
				t.Fatal("failed startup reported success")
			}
			if stage == "hook" && !errors.Is(err, failure) {
				t.Fatal("lost startup error", err)
			}
			if (s.healthChecker != nil) != (stage == "bind") {
				t.Fatal("services started out of hook order")
			}
			if s.healthChecker != nil {
				select {
				case <-s.healthChecker.done:
				default:
					t.Fatal("health worker survived startup failure")
				}
				select {
				case <-s.usageRetention.done:
				default:
					t.Fatal("retention worker survived startup failure")
				}
			}
			if s.usageWriterSnapshot() != nil || s.usageWriterStats().Closed != (stage != "hook") {
				t.Fatal("usage writer was not started/drained in hook order")
			}
			if err := s.store.Ping(context.Background()); err == nil {
				t.Fatal("failed startup left storage open")
			}
			if s.shutdownErr != nil {
				t.Fatal(s.shutdownErr)
			}
		})
	}
}

func TestShutdownSignalWaitsForResponseCompletion(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		name := "success"
		if failWrite {
			name = "write_failure"
		}
		t.Run(name, func(t *testing.T) {
			s := &Server{}
			e := newTestEngine(t)
			e.Use(s.requestLifecycle())
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			const acknowledgment = `{"shuttingDown":true}`
			e.POST("/shutdown", func(ctx context.Context, c *app.RequestContext) {
				s.shutdown(ctx, c)
				// Gate actual response serialization after the handler returns.
				var body io.Reader = strings.NewReader(string(c.Response.Body()))
				if failWrite {
					body = iotest.ErrReader(errors.New("acknowledgment write failed"))
				}
				c.SetBodyStream(io.MultiReader(&heldHTTPBody{started: started, release: release}, body), len(acknowledgment))
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			result := make(chan error, 1)
			go func() {
				response, err := web.Client().Post(web.URL+"/shutdown", "application/json", nil)
				if err == nil {
					defer response.Body.Close()
					var body []byte
					body, err = io.ReadAll(response.Body)
					if err == nil && (response.StatusCode != 200 || string(body) != acknowledgment) {
						err = fmt.Errorf("unexpected acknowledgment: %d %s", response.StatusCode, body)
					}
				}
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("response serialization did not start")
			}
			select {
			case <-s.shutdownRequested:
				t.Fatal("shutdown signaled before sending acknowledgment")
			default:
			}
			unblock()
			select {
			case <-s.shutdownRequested:
			case <-time.After(3 * time.Second):
				t.Fatal("response completion did not signal shutdown")
			}
			select {
			case err := <-result:
				if (err != nil) != failWrite {
					t.Fatalf("response error = %v, failWrite = %t", err, failWrite)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client did not finish")
			}
		})
	}
}

func TestHTTPCompletionPanicStillReleasesRequest(t *testing.T) {
	s := &Server{}
	e := newTestEngine(t)
	e.Use(s.requestLifecycle())
	released := make(chan struct{})
	e.GET("/complete", func(_ context.Context, c *app.RequestContext) {
		onHTTPComplete(c, func() { close(released) })
		onHTTPComplete(c, func() { panic("first completion failure") })
		onHTTPComplete(c, func() { panic("second completion failure") })
		c.String(200, "done")
	})
	web := startTestServer(e)
	defer web.Close()
	response, err := web.Client().Get(web.URL + "/complete")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "done" {
		t.Fatal(response.StatusCode, string(body), err)
	}
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("panic skipped a resource release")
	}
	s.httpRequests.stop()
	drained := make(chan struct{})
	go func() { s.httpRequests.wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("panic leaked the request lifecycle lease")
	}
}

func TestShutdownForceClosesStuckHTTPConnection(t *testing.T) {
	for _, stage := range []string{"handler", "response_body"} {
		t.Run(stage, func(t *testing.T) {
			s := newKeyPermissionTestServer(t)
			s.initLifecycle()
			s.engine = newTestEngine(t)
			s.engine.Use(s.requestLifecycle())
			started, release := make(chan struct{}), make(chan struct{})
			s.engine.GET("/stuck", func(ctx context.Context, c *app.RequestContext) {
				if stage == "response_body" {
					c.SetBodyStream(io.MultiReader(strings.NewReader("started"), &heldHTTPBody{started: started, release: release}), -1)
					return
				}
				w := startStream(c)
				_, _ = w.Write([]byte("started"))
				_ = w.Flush()
				close(started)
				<-release
			})
			web := startTestServer(s.engine)
			defer web.Close()
			connectionClosed := make(chan struct{})
			go func() {
				defer close(connectionClosed)
				if resp, err := http.Get(web.URL + "/stuck"); err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}()
			<-started
			done := make(chan struct{})
			go func() {
				s.doShutdown()
				close(done)
			}()
			select {
			case <-connectionClosed:
			case <-time.After(7 * time.Second):
				t.Error("HTTP shutdown deadline did not force-close the active connection")
			}
			if err := s.store.Ping(context.Background()); err != nil {
				t.Errorf("storage closed before the active handler released it: %v", err)
			}
			close(release)
			<-done
			if err := s.store.Ping(context.Background()); err == nil {
				t.Error("storage remained open after the last handler exited")
			}
		})
	}
}

// Simulates a reader retained by Hertz after its route handler has returned.
type heldHTTPBody struct {
	started chan struct{}
	release <-chan struct{}
}

func (r *heldHTTPBody) Read([]byte) (int, error) {
	close(r.started)
	<-r.release
	return 0, io.EOF
}
