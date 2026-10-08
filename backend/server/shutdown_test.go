package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
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
		source := storage.ModelSource{ID: string(rune('a' + i)), BaseURL: upstream.URL, Platform: "openai", AutoFetchModels: true}
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
	s.engine = gin.New()
	started, handled := make(chan struct{}), make(chan struct{})
	s.engine.GET("/stream", func(c *gin.Context) {
		close(started)
		<-c.Request.Context().Done()
		s.recordUsage(&usageRecord{RequestID: "last-http-call", StartedAt: time.Now(), EndedAt: time.Now(), StatusCode: 499})
		close(handled)
	})
	web := httptest.NewUnstartedServer(http.HandlerFunc(s.serveHTTP))
	web.Config.BaseContext = func(net.Listener) context.Context { return s.lifecycleCtx }
	s.httpServer = web.Config
	web.Start()
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
		Server:             config.ServerConfig{Host: "127.0.0.1"},
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

func TestShutdownForceClosesStuckHTTPConnection(t *testing.T) {
	s := newKeyPermissionTestServer(t)
	s.initLifecycle()
	s.engine = gin.New()
	started, release := make(chan struct{}), make(chan struct{})
	s.engine.GET("/stuck", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		close(started)
		<-release
	})
	web := httptest.NewUnstartedServer(http.HandlerFunc(s.serveHTTP))
	web.Config.BaseContext = func(net.Listener) context.Context { return s.lifecycleCtx }
	s.httpServer = web.Config
	web.Start()
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
	go func() { s.doShutdown(); close(done) }()
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
}
