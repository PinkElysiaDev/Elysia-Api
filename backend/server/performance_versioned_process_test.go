package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

// TestVersionedGatewayProcess adds process-local counters around the normal
// application lifecycle. It is copied unchanged into historical test builds;
// standalone release builds never contain this collector or its HTTP endpoint.
func TestVersionedGatewayProcess(t *testing.T) {
	path := os.Getenv("ELYSIA_VERSIONED_CONFIG")
	if path == "" {
		t.Skip("opt-in isolated application process")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := New(cfg)
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	defer func() {
		server.RequestShutdown()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(30 * time.Second):
			t.Error("application shutdown timeout")
		}
	}()
	var mutex sync.Mutex
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		if r.URL.Query().Get("action") == "reset-without-gc" {
			before = memory
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Allocated":   memory.TotalAlloc - before.TotalAlloc,
			"Allocations": memory.Mallocs - before.Mallocs,
			"Heap":        memory.HeapAlloc, "GCCycles": memory.NumGC - before.NumGC,
			"GCPauseNS":  memory.PauseTotalNs - before.PauseTotalNs,
			"Goroutines": runtime.NumGoroutine(),
		})
	}))
	defer metrics.Close()
	ready, _ := json.Marshal(map[string]any{"Metrics": metrics.URL, "PID": os.Getpid()})
	if err := os.WriteFile(os.Getenv("ELYSIA_VERSIONED_READY"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
