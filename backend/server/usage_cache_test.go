package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
)

// TestUsageCacheCoalescesAndServes：并发同 key 请求合并为一次回源、缓存命中
// 直接回放、参数顺序不同视为同 key。
func TestUsageCacheCoalescesAndServes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cache := &usageResponseCache{}
		var executions int32
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()

		r := gin.New()
		r.GET("/p", cache.middleware(), func(c *gin.Context) {
			atomic.AddInt32(&executions, 1)
			<-release
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		// Wait until every request is blocked: one leader in the handler and the
		// remaining callers in the cache. This proves coalescing, not just cache hits.
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
		synctest.Wait()
		if got := atomic.LoadInt32(&executions); got != 1 {
			t.Fatalf("expected exactly one blocked leader, got %d", got)
		}
		unblock()
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
	})
}

// Failed and empty responses must neither be shared with waiters nor cached.
func TestUsageCacheSkipsUncacheableResponses(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusInternalServerError, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cache := &usageResponseCache{}
				var executions atomic.Int32
				release := make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				r := gin.New()
				r.GET("/e", cache.middleware(), func(c *gin.Context) {
					executions.Add(1)
					<-release
					if status == http.StatusOK {
						c.Status(status)
					} else {
						c.JSON(status, gin.H{"error": "boom"})
					}
				})
				request := func() {
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
					if w.Code != status {
						t.Errorf("response status = %d, want %d", w.Code, status)
					}
				}
				var wg sync.WaitGroup
				for range 3 {
					wg.Go(request)
				}
				synctest.Wait()
				if got := executions.Load(); got != 1 {
					t.Fatalf("expected one blocked leader, got %d", got)
				}
				unblock()
				wg.Wait()
				request()
				if got := executions.Load(); got != 4 {
					t.Fatalf("uncacheable response reused: executions = %d, want 4", got)
				}
			})
		})
	}
}

// New-generation callers must not join an old flight; old waiters must not
// replay its result or allow it to overwrite the new cache entry.
func TestUsageCacheFlushIsolatesFlights(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cache := &usageResponseCache{}
		var executions atomic.Int32
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		r := gin.New()
		r.GET("/p", cache.middleware(), func(c *gin.Context) {
			if executions.Add(1) == 1 {
				<-release
				c.JSON(http.StatusOK, gin.H{"gen": 1})
				return
			}
			c.JSON(http.StatusOK, gin.H{"gen": 2})
		})
		request := func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
			return w
		}
		var leader, waiter *httptest.ResponseRecorder
		var wg sync.WaitGroup
		wg.Go(func() { leader = request() })
		synctest.Wait()
		wg.Go(func() { waiter = request() })
		synctest.Wait()
		if executions.Load() != 1 {
			t.Fatal("waiter did not join the old flight")
		}
		cache.flush()
		fresh := request() // Must complete while the old leader is still blocked.
		if fresh.Code != http.StatusOK || !strings.Contains(fresh.Body.String(), `"gen":2`) {
			t.Fatalf("new generation response = %d %s", fresh.Code, fresh.Body.String())
		}
		unblock()
		wg.Wait()
		if !strings.Contains(leader.Body.String(), `"gen":1`) || waiter.Body.String() != fresh.Body.String() {
			t.Fatalf("generation isolation failed: leader=%s waiter=%s", leader.Body.String(), waiter.Body.String())
		}
		if cached := request(); cached.Body.String() != fresh.Body.String() || executions.Load() != 3 {
			t.Fatalf("old flight replaced the fresh cache: body=%s executions=%d", cached.Body.String(), executions.Load())
		}
	})
}

// Exercise panic propagation through Gin, then recovery of the same cache key.
func TestUsageCacheSurvivesHandlerPanic(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cache := &usageResponseCache{}
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		fail := true
		r := gin.New()
		r.Use(gin.RecoveryWithWriter(io.Discard))
		r.GET("/p", cache.middleware(), func(c *gin.Context) {
			<-release
			if fail {
				panic("boom")
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		request := func(status int) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
			if w.Code != status {
				t.Errorf("response status = %d, want %d", w.Code, status)
			}
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() { request(http.StatusInternalServerError) })
		}
		synctest.Wait()
		unblock()
		wg.Wait()
		fail = false
		request(http.StatusOK)
	})
}

func TestUsageCacheKeyEscaping(t *testing.T) {
	t.Parallel()
	left, _ := url.Parse("/p?groupName=b%26keyName%3Da")
	right, _ := url.Parse("/p?groupName=b&keyName=a")
	if usageCacheKey(left) == usageCacheKey(right) {
		t.Fatal("distinct filter sets must not collide")
	}
}
