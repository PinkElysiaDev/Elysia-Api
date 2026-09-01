package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// leader handler panic 时兜底清理仍须执行：等待者不挂起、后续同 key 请求
// 正常回源（gin.Recovery 在上层恢复，缓存自身必须自愈）。
func TestUsageCacheSurvivesHandlerPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &usageResponseCache{}
	var attempts int32

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/p", cache.middleware(), func(c *gin.Context) {
		n := attempts
		attempts = n + 1
		if n == 0 {
			panic("boom")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 第一次请求触发 leader panic（Recovery 转成 500，缓存层完成兜底清理）。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p?a=1", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("first request should surface the panic as 500, got %d", w.Code)
	}

	// 缓存层必须已清理 inflight：同 key 请求立即回源成功而非挂起。
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p?a=1", nil))
		done <- rec
	}()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("post-panic request should succeed, got %d: %s", rec.Code, rec.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("post-panic request hung: inflight entry was not cleaned up")
	}

	// 并发等待者场景：慢 leader panic，多个等待者都必须返回而非永久阻塞。
	slowPanic := gin.New()
	slowPanic.Use(gin.Recovery())
	slowPanic.GET("/q", cache.middleware(), func(c *gin.Context) {
		time.Sleep(80 * time.Millisecond)
		panic("slow boom")
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			slowPanic.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/q?b=2", nil))
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("waiter got %d, want 500", rec.Code)
			}
		}()
	}
	wg.Wait()
}

// 缓存键值转义：含分隔符的筛选值不得与多参数组合碰撞。
func TestUsageCacheKeyEscaping(t *testing.T) {
	left := usageCacheKey(mustParseURL(t, "/p?groupName=b%26keyName%3Da"))
	right := usageCacheKey(mustParseURL(t, "/p?groupName=b&keyName=a"))
	if left == right {
		t.Fatalf("distinct filter sets must not collide: %q == %q", left, right)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}
