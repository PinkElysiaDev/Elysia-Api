package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/storage"
	"github.com/rs/zerolog"
)

func TestShutdownDrainsBothLogQueues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "business.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, config: &config.Config{}}
	s.logger = logging.New(logging.Options{Level: "error"}, logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	t.Cleanup(s.doShutdown)
	s.startUsageWriter()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				id := fmt.Sprintf("%d-%d", worker, j)
				s.recordUsage(&usageRecord{RequestID: id, StartedAt: time.Now(), StatusCode: 200})
				s.logSystemEvent("info", "completed", map[string]any{"request_id": id})
			}
		}(i)
	}
	wg.Wait()
	s.doShutdown()
	if s.shutdownErr != nil {
		t.Fatal(s.shutdownErr)
	}
	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if count, err := reopened.CountUsageRecords(t.Context()); err != nil || count != 512 {
		t.Fatalf("usage count %d: %v", count, err)
	}
	if count, _, err := reopened.QuerySystemLogs(t.Context(), 1, 0, ""); err != nil || count != 513 {
		t.Fatalf("system count %d: %v", count, err)
	}
	if s.Logger().Stats().RetainedBytes != 0 || s.usageWriterStats().RetainedBytes != 0 {
		t.Fatal("shutdown retained queued data")
	}
}

func TestRequestLoggerCorrelatesAndRecovers(t *testing.T) {
	s := &Server{logger: logging.New(logging.Options{Level: "debug", Format: "json"}, nil)}
	var output bytes.Buffer
	s.logger.SetOutput(&output)
	engine := newTestEngine(t)
	engine.Use(s.requestLogging())
	var id string
	engine.GET("/panic", func(ctx context.Context, c *app.RequestContext) {
		id = c.GetString("requestID")
		zerolog.Ctx(ctx).Info().Msg("in handler")
		panic("test panic")
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.Header.Set("X-Elysia-Request-Id", "untrusted")
	serveTestHTTP(engine, w, r)
	if w.Code != 500 || id == "" || id == "untrusted" || w.Header().Get("X-Elysia-Request-Id") != id {
		t.Fatalf("status/id: %d %q", w.Code, id)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := sonic.ConfigStd.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["request_id"] != id {
			t.Fatalf("uncorrelated event: %s", line)
		}
	}
}

type requestLogEvents chan []byte

func (events requestLogEvents) Write(data []byte) (int, error) {
	events <- bytes.Clone(data)
	return len(data), nil
}

type loggedResponseBody struct {
	started chan struct{}
	release <-chan struct{}
	err     error
}

func (body *loggedResponseBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.release
	return 0, body.err
}

func TestRequestLoggerWaitsForResponseCompletion(t *testing.T) {
	for _, bodyErr := range []error{io.EOF, errors.New("response reader failed")} {
		t.Run(bodyErr.Error(), func(t *testing.T) {
			s := &Server{logger: logging.New(logging.Options{Level: "debug", Format: "json"}, nil)}
			events := make(requestLogEvents, 4)
			s.logger.SetOutput(events)
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			e := newTestEngine(t)
			e.Use(s.requestLogging())
			e.GET("/body", func(_ context.Context, c *app.RequestContext) {
				length := -1
				if bodyErr != io.EOF {
					// Production assets have a known length. Hertz's unknown-length
					// reader path logs reader errors internally instead of returning them.
					length = 1
				}
				c.SetBodyStream(&loggedResponseBody{started: started, release: release, err: bodyErr}, length)
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			clientDone := make(chan struct{})
			go func() {
				defer close(clientDone)
				if response, err := web.Client().Get(web.URL + "/body"); err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("Hertz did not start the response reader")
			}
			select {
			case event := <-events:
				t.Fatalf("request logged before body finished: %s", event)
			default:
			}
			unblock()
			select {
			case data := <-events:
				var event map[string]any
				if err := sonic.ConfigStd.Unmarshal(data, &event); err != nil {
					t.Fatal(err)
				}
				if event["message"] != "request completed" || event["status_code"] != float64(200) {
					t.Fatal(string(data))
				}
				if bodyErr == io.EOF {
					if event["level"] != "debug" || event["error"] != nil {
						t.Fatal(string(data))
					}
				} else if detail, _ := event["error"].(string); event["level"] != "error" || !strings.Contains(detail, bodyErr.Error()) {
					t.Fatal("lost post-handler transport error", string(data))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing completion log")
			}
			select {
			case <-clientDone:
			case <-time.After(3 * time.Second):
				t.Fatal("response did not finish")
			}
		})
	}
}

func TestShutdownReportsPersistenceFailure(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "failed.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, config: &config.Config{}}
	s.logger = logging.New(logging.Options{Level: "error"}, logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	s.logSystemEvent("info", "cannot persist", nil)
	s.doShutdown()
	if s.shutdownErr == nil || s.Logger().Stats().Failures == 0 {
		t.Fatal("failed drain reported successful shutdown")
	}
	if err := s.logger.Close(context.Background()); err == nil {
		t.Fatal("idempotent close lost the persistence error")
	}
}

func TestRuntimeLogSettingsAreAtomicAndPathsRequireRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"logLevel":"info"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg, logger: logging.New(cfg.GetLoggingOptions(), nil)}
	alias := cfg.GetDatabasePath()
	if _, err := s.updateRuntimeConfig(runtimeConfigPayload{LogDatabasePath: &alias, LogLevel: "debug"}); err == nil {
		t.Fatal("accepted business database as log database")
	} else if invalid, ok := err.(*runtimeConfigError); !ok || invalid.status != http.StatusBadRequest || invalid.code != "invalid_database_path" {
		t.Fatalf("path validation lost its HTTP error semantics: %v", err)
	}
	if cfg.GetLogLevel() != "info" {
		t.Fatal("invalid path published partial logger configuration")
	}
	logs := filepath.Join(dir, "separate", "logs.db")
	restart, err := s.updateRuntimeConfig(runtimeConfigPayload{LogDatabasePath: &logs, LogLevel: "error", LogPersistLevel: "warn", LogFormat: "json"})
	if err != nil || !restart {
		t.Fatalf("path update: restart=%t err=%v", restart, err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetLogDatabasePath() != logs || loaded.GetLoggingOptions().PersistLevel != "warn" || loaded.GetLoggingOptions().Format != "json" {
		t.Fatal("logging settings did not persist")
	}
	var console bytes.Buffer
	s.logger.SetOutput(&console)
	s.logEvent("info").Msg("filtered")
	if console.Len() != 0 {
		t.Fatal("console threshold was not reloaded")
	}
	restart, err = s.updateRuntimeConfig(runtimeConfigPayload{LogLevel: "debug"})
	if err != nil || restart {
		t.Fatalf("level update required restart: %t %v", restart, err)
	}
	s.logger.SetOutput(&console)
	s.logEvent("debug").Msg("visible")
	if !bytes.Contains(console.Bytes(), []byte("visible")) {
		t.Fatal("debug threshold was not applied")
	}
}
