package server

import (
	"bytes"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maypok86/otter/v2"
	"golang.org/x/sync/singleflight"
)

// usage 只读端点的短 TTL 响应缓存。切窗三连发与 overview 重叠窗口的并发
// GET 合并成一次 SQL；写入成功后 flush，避免新调用仍命中旧响应。
const (
	usageCacheTTL = 15 * time.Second
	// Otter maintains this target asynchronously; it is not a hard memory bound.
	usageCacheMaxEntries = 256
)

type usageCacheEntry struct {
	body        []byte
	contentType string
	statusCode  int
}

// usageResponseCache 缓存 usage 只读端点的响应字节，并把并发同 key 请求
// 合并为一次回源。失效代际同时隔离缓存写回与 singleflight 调用。
type usageResponseCache struct {
	mu      sync.Mutex // Serializes publication with invalidation and initialization.
	gen     atomic.Uint64
	entries atomic.Pointer[otter.Cache[string, *usageCacheEntry]]
	closed  bool
	flights singleflight.Group
}

// Keep the zero value usable by lightweight servers and initialize only when a
// read endpoint is used. Cache hits do not acquire the publication mutex.
func (c *usageResponseCache) cache() *otter.Cache[string, *usageCacheEntry] {
	if entries := c.entries.Load(); entries != nil {
		return entries
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if c.entries.Load() == nil {
		c.entries.Store(otter.Must(&otter.Options[string, *usageCacheEntry]{
			MaximumSize:      usageCacheMaxEntries,
			ExpiryCalculator: otter.ExpiryWriting[string, *usageCacheEntry](usageCacheTTL),
		}))
	}
	return c.entries.Load()
}

func (c *usageResponseCache) middleware() gin.HandlerFunc {
	return c.handle
}

func (c *usageResponseCache) handle(gc *gin.Context) {
	if gc.Request.Method != http.MethodGet {
		gc.Next()
		return
	}
	key := usageCacheKey(gc.Request.URL)
	entries := c.cache()
	if entries == nil {
		gc.Next()
		return
	}
	gen := c.gen.Load()
	if e, ok := entries.GetIfPresent(key); ok {
		serveUsageCacheEntry(gc, e)
		gc.Abort()
		return
	}

	// Do runs the leader inline: Gin's context/writer stay on their request
	// goroutine, and panics reach gin.Recovery for both leader and waiters.
	handled := false
	value, _, _ := c.flights.Do(strconv.FormatUint(gen, 10)+":"+key, func() (any, error) {
		if c.gen.Load() != gen {
			return nil, nil
		}
		// Another flight may have populated the cache since our initial lookup.
		if e, ok := entries.GetIfPresent(key); ok {
			return e, nil
		}

		handled = true
		rec := &usageCacheRecorder{ResponseWriter: gc.Writer}
		rec.buf.Grow(512)
		gc.Writer = rec
		defer func() { gc.Writer = rec.ResponseWriter }()
		gc.Next()

		entry := &usageCacheEntry{
			body:        rec.buf.Bytes(),
			contentType: rec.Header().Get("Content-Type"),
			statusCode:  rec.Status(),
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if entry.statusCode == http.StatusOK && len(entry.body) > 0 && c.gen.Load() == gen && !c.closed {
			entries.Set(key, entry)
			return entry, nil
		}
		return nil, nil
	})
	if handled {
		return // The leader has already written its own response.
	}
	if entry, ok := value.(*usageCacheEntry); ok && c.gen.Load() == gen {
		serveUsageCacheEntry(gc, entry)
		gc.Abort()
		return
	}
	// Failed or invalidated flights are not replayed to other requests.
	gc.Next()
}

func serveUsageCacheEntry(gc *gin.Context, e *usageCacheEntry) {
	gc.Data(e.statusCode, e.contentType, e.body)
}

// flush 清空全部缓存条目并递增 generation，使进行中的回源无法把旧响应写回。
func (c *usageResponseCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entries := c.entries.Load(); entries != nil {
		entries.InvalidateAll()
	}
	// Publish the new generation only after old entries have been removed.
	c.gen.Add(1)
}

func (c *usageResponseCache) close() {
	c.mu.Lock()
	c.closed = true
	entries := c.entries.Load()
	c.mu.Unlock()
	if entries != nil {
		entries.StopAllGoroutines()
	}
}

// usageCacheRecorder 捕获写入的响应字节，同时保持原 writer 行为（状态码、
// header 均由内嵌 writer 维护）。
type usageCacheRecorder struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

func (r *usageCacheRecorder) Write(b []byte) (int, error) {
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *usageCacheRecorder) WriteString(s string) (int, error) {
	r.buf.WriteString(s)
	return r.ResponseWriter.WriteString(s)
}

// usageCacheKey 以 path + 排序后的 query 生成缓存键：参数顺序不同
// 但语义相同的请求共享同一条缓存。值经转义，防止 "b&keyName=a" 这类含分隔
// 符的筛选拼接出与多参数组合相同的键。
func usageCacheKey(u *url.URL) string {
	values := u.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(u.Path)
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			b.WriteByte('&')
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}
