package server

import (
	"bytes"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// usage 只读端点的短 TTL 响应缓存。切窗三连发与 overview 重叠窗口的并发
// GET 合并成一次 SQL；写入成功后 flush，避免新调用仍命中旧响应。
const (
	usageCacheTTL        = 15 * time.Second
	usageCacheMaxEntries = 256
)

type usageCacheEntry struct {
	body        []byte
	contentType string
	statusCode  int
	expiresAt   time.Time
}

// usageResponseCache 缓存 usage 只读端点的响应字节，并把并发同 key 请求
// 合并为一次回源。失效代际同时隔离缓存写回与 singleflight 调用。
type usageResponseCache struct {
	mu      sync.Mutex
	gen     uint64 // flush 递增，禁止 reset 前启动的回源写回
	entries map[string]*usageCacheEntry
	order   []string // 插入序，容量超限时淘汰最旧
	flights singleflight.Group
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

	c.mu.Lock()
	gen := c.gen
	e := c.entryLocked(key)
	c.mu.Unlock()
	if e != nil {
		serveUsageCacheEntry(gc, e)
		gc.Abort()
		return
	}

	// Do runs the leader inline: Gin's context/writer stay on their request
	// goroutine, and panics reach gin.Recovery for both leader and waiters.
	handled := false
	value, _, _ := c.flights.Do(strconv.FormatUint(gen, 10)+":"+key, func() (any, error) {
		c.mu.Lock()
		current := c.gen == gen
		e := c.entryLocked(key)
		c.mu.Unlock()
		if !current {
			return nil, nil
		}
		// Another flight may have populated the cache since our initial lookup.
		if e != nil {
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
			expiresAt:   time.Now().Add(usageCacheTTL),
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if entry.statusCode == http.StatusOK && len(entry.body) > 0 && c.gen == gen {
			c.storeEntryLocked(key, entry)
			return entry, nil
		}
		return nil, nil
	})
	if handled {
		return // The leader has already written its own response.
	}
	c.mu.Lock()
	current := c.gen == gen
	c.mu.Unlock()
	if entry, ok := value.(*usageCacheEntry); ok && current {
		serveUsageCacheEntry(gc, entry)
		gc.Abort()
		return
	}
	// Failed or invalidated flights are not replayed to other requests.
	gc.Next()
}

func (c *usageResponseCache) entryLocked(key string) *usageCacheEntry {
	if entry := c.entries[key]; entry != nil && time.Now().Before(entry.expiresAt) {
		return entry
	}
	return nil
}

func serveUsageCacheEntry(gc *gin.Context, e *usageCacheEntry) {
	gc.Data(e.statusCode, e.contentType, e.body)
}

func (c *usageResponseCache) storeEntryLocked(key string, e *usageCacheEntry) {
	if c.entries == nil {
		c.entries = make(map[string]*usageCacheEntry)
	}
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = e
	for len(c.order) > usageCacheMaxEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// flush 清空全部缓存条目并递增 generation，使进行中的回源无法把旧响应写回。
func (c *usageResponseCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = nil
	c.order = nil
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
