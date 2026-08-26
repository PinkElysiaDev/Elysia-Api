package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestUsageCacheCoalescesAndServes：并发同 key 请求合并为一次回源、缓存命中
// 直接回放、参数顺序不同视为同 key、flush 后重新回源。
func TestUsageCacheCoalescesAndServes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &usageResponseCache{}
	var executions int32

	r := gin.New()
	r.GET("/p", cache.middleware(), func(c *gin.Context) {
		atomic.AddInt32(&executions, 1)
		time.Sleep(50 * time.Millisecond)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 5 个并发同 key 请求：慢 handler 期间全部到达，应只回源一次。
	var wg sync.WaitGroup
	bodies := make([]string, 5)
	codes := make([]int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p?a=1&b=2", nil))
			bodies[i] = w.Body.String()
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&executions); got != 1 {
		t.Fatalf("concurrent identical requests should coalesce to 1 execution, got %d", got)
	}
	for i := range bodies {
		if codes[i] != http.StatusOK || bodies[i] != bodies[0] || bodies[i] == "" {
			t.Fatalf("waiter %d got code=%d body=%q", i, codes[i], bodies[i])
		}
	}

	// TTL 内再次请求（参数顺序不同）：命中缓存，不回源。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p?b=2&a=1", nil))
	if w.Code != http.StatusOK || w.Body.String() != bodies[0] {
		t.Fatalf("cache hit mismatch: code=%d body=%q", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&executions); got != 1 {
		t.Fatalf("cached request must not re-execute, got %d executions", got)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("cached response must keep Content-Type")
	}

	// flush 后重新回源。
	cache.flush()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p?a=1&b=2", nil))
	if got := atomic.LoadInt32(&executions); got != 2 {
		t.Fatalf("after flush request should re-execute, got %d executions", got)
	}
}

// TestUsageCacheSkipsNonSuccess：非 200 响应不缓存，后续请求重新回源。
func TestUsageCacheSkipsNonSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &usageResponseCache{}
	var executions int32

	r := gin.New()
	r.GET("/e", cache.middleware(), func(c *gin.Context) {
		atomic.AddInt32(&executions, 1)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "boom"})
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("request %d: expected 500, got %d", i, w.Code)
		}
	}
	if got := atomic.LoadInt32(&executions); got != 2 {
		t.Fatalf("non-200 responses must not be cached, got %d executions", got)
	}
}
