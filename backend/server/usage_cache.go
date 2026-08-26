package server

import (
	"bytes"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// usage 只读端点的短 TTL 响应缓存。usage 数据只追加、前端请求参数本就按
// 5 分钟桶化，15s TTL 足以把切窗三连发（stats/trend/by-model）与 overview
// 重叠窗口的并发请求合并成单次 SQL，同时不引入可感知的滞后。
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

// usageCacheCall 是一次回源执行：并发同 key 请求等待 leader 完成后共享其响应。
type usageCacheCall struct {
	done  chan struct{}
	entry *usageCacheEntry // 非 200 回源不缓存，等待者 entry 为 nil
}

// usageResponseCache 缓存 usage 只读端点的响应字节，并把并发同 key 请求
// 合并为一次回源（手写 singleflight，避免为此引入依赖）。
type usageResponseCache struct {
	mu       sync.Mutex
	entries  map[string]*usageCacheEntry
	order    []string // 插入序，容量超限时淘汰最旧
	inflight map[string]*usageCacheCall
}

func (c *usageResponseCache) initLocked() {
	if c.entries == nil {
		c.entries = make(map[string]*usageCacheEntry)
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*usageCacheCall)
	}
}

func (c *usageResponseCache) middleware() gin.HandlerFunc {
	return c.handle
}

func (c *usageResponseCache) handle(gc *gin.Context) {
	if gc.Request.Method != http.MethodGet {
		gc.Next()
		return
	}
	key := canonicalUsageCacheKey(gc.Request.URL)

	c.mu.Lock()
	c.initLocked()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		c.mu.Unlock()
		serveUsageCacheEntry(gc, e)
		gc.Abort()
		return
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-call.done
		if call.entry != nil {
			serveUsageCacheEntry(gc, call.entry)
			gc.Abort()
			return
		}
		// 回源失败（非 200）：等待者直接透传执行，不缓存错误响应。
		gc.Next()
		return
	}
	call := &usageCacheCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	rec := &usageCacheRecorder{ResponseWriter: gc.Writer}
	rec.buf.Grow(512)
	gc.Writer = rec
	gc.Next()

	entry := &usageCacheEntry{
		body:        rec.buf.Bytes(),
		contentType: rec.Header().Get("Content-Type"),
		statusCode:  rec.Status(),
		expiresAt:   time.Now().Add(usageCacheTTL),
	}
	if entry.statusCode == http.StatusOK && len(entry.body) > 0 {
		c.storeEntry(key, entry)
	} else {
		entry = nil
	}
	call.entry = entry
	close(call.done)
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
}

func serveUsageCacheEntry(gc *gin.Context, e *usageCacheEntry) {
	gc.Data(e.statusCode, e.contentType, e.body)
}

func (c *usageResponseCache) storeEntry(key string, e *usageCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
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

// flush 清空全部缓存条目（usage 重置后调用，避免返回已删除数据的旧响应）。
func (c *usageResponseCache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// canonicalUsageCacheKey 以 path + 排序后的 query 生成缓存键：
// 参数顺序不同但语义相同的请求共享同一条缓存。
func canonicalUsageCacheKey(u *url.URL) string {
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
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
	}
	return b.String()
}
