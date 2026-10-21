package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

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

func (c *usageResponseCache) middleware() app.HandlerFunc {
	return c.handle
}

func (c *usageResponseCache) handle(ctx context.Context, gc *app.RequestContext) {
	if string(gc.Method()) != http.MethodGet {
		gc.Next(ctx)
		return
	}
	u, _ := url.Parse(string(gc.Request.URI().RequestURI()))
	key := usageCacheKey(u)
	entries := c.cache()
	if entries == nil {
		gc.Next(ctx)
		return
	}
	gen := c.gen.Load()
	if e, ok := entries.GetIfPresent(key); ok {
		serveUsageCacheEntry(gc, e)
		gc.Abort()
		return
	}

	// Run the leader inline; the request context stays on its owning goroutine.
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
		gc.Next(ctx)

		entry := &usageCacheEntry{
			body:        bytes.Clone(gc.Response.Body()),
			contentType: string(gc.Response.Header.ContentType()),
			statusCode:  gc.Response.StatusCode(),
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
	gc.Next(ctx)
}

func serveUsageCacheEntry(gc *app.RequestContext, e *usageCacheEntry) {
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

// usageCacheKey 以 path + 排序后的 query 生成缓存键：参数顺序不同
// 但语义相同的请求共享同一条缓存。值经转义，防止 "b&keyName=a" 这类含分隔
// 符的筛选拼接出与多参数组合相同的键。
func usageCacheKey(u *url.URL) string {
	values := u.Query()
	for _, items := range values {
		slices.Sort(items)
	}
	return u.Path + "?" + values.Encode()
}
