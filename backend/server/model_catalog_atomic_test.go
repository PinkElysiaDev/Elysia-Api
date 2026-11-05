package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

func TestModelCatalogCacheAtomicFailureAndPrivateStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	// The old fixed .tmp pathname could clobber an unrelated file or collide
	// with another writer. The library must choose its own exclusive name.
	if err := os.WriteFile(path+".tmp", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := &modelCatalog{cachePathGetter: func() string { return path }, count: 7}
	catalog.saveToCache([]byte(`{"source":{"models":{}}}`), time.Now())
	if catalog.lastError != "" {
		t.Fatal(catalog.lastError)
	}
	data, err := os.ReadFile(path + ".tmp")
	if err != nil || string(data) != "keep" {
		t.Fatalf("clobbered fixed staging file: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	catalog.saveToCache([]byte(`{}`), time.Now())
	if !strings.Contains(catalog.lastError, "cache write:") || catalog.count != 7 {
		t.Fatalf("rename failure not reported or memory changed: %+v", catalog)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("temporary files survived failure: %v %v", files, err)
	}
}

func TestModelCatalogRefreshOwnsCacheWrite(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"local":{"models":{"fixture":{"tool_call":true}}}}`))
	}))
	defer upstream.Close()
	catalog := newModelCatalog(func() config.ModelCatalogConfig { return config.ModelCatalogConfig{URL: upstream.URL} }, nil)
	defer catalog.shutdown()
	cacheStarted, releaseCache := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCache) }) }
	defer release()
	cachePath := filepath.Join(t.TempDir(), "catalog.json")
	catalog.cachePathGetter = func() string {
		startOnce.Do(func() { close(cacheStarted) })
		<-releaseCache
		return cachePath
	}
	if !catalog.triggerRefreshIfNeeded(true) {
		t.Fatal("initial refresh did not start")
	}
	select {
	case <-cacheStarted:
	case <-time.After(time.Second):
		t.Fatal("refresh did not reach cache persistence")
	}
	if catalog.triggerRefreshIfNeeded(true) {
		t.Fatal("another refresh started while the first still owned persistence")
	}
	type refreshResult struct {
		status   map[string]any
		didFetch bool
	}
	result := make(chan refreshResult, 1)
	go func() {
		status, didFetch := catalog.Refresh()
		result <- refreshResult{status, didFetch}
	}()
	select {
	case <-result:
		t.Fatal("synchronous refresh returned before persistence settled")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case result := <-result:
		if result.didFetch || result.status["entries"] != 1 || calls.Load() != 1 {
			t.Fatal("concurrent refresh did not share the completed fetch", result, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("synchronous refresh did not observe completion")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal("refresh completed without its cache", err)
	}
}
