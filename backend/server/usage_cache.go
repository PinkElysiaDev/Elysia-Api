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

// usageCacheCall 是一次回源执行：并发同 key 请求等待 leader 完成后共享其响应。
type usageCacheCall struct {
	done  chan struct{}
	entry *usageCacheEntry // 非 200 回源不缓存，等待者 entry 为 nil
	gen   uint64
}

// usageResponseCache 缓存 usage 只读端点的响应字节，并把并发同 key 请求
// 合并为一次回源（手写 singleflight，避免为此引入依赖）。
type usageResponseCache struct {
	mu       sync.Mutex
	gen      uint64 // flush 递增，禁止 reset 前启动的回源写回
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
	gen := c.gen
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		c.mu.Unlock()
		serveUsageCacheEntry(gc, e)
		gc.Abort()
		return
	}
	if call, ok := c.inflight[key]; ok && call.gen == gen {
		c.mu.Unlock()
		<-call.done
		c.mu.Lock()
		cur := c.gen
		c.mu.Unlock()
		if call.entry != nil && call.gen == cur {
			serveUsageCacheEntry(gc, call.entry)
			gc.Abort()
			return
		}
		// 回源失败或 flush 后过期：等待者自己执行，不复用旧响应。
		gc.Next()
		return
	}
	call := &usageCacheCall{done: make(chan struct{}), gen: gen}
	c.inflight[key] = call
	c.mu.Unlock()

	// 兜底清理：handler panic 时（gin.Recovery 在本帧之上恢复）done 关闭与
	// inflight 清理仍会执行，等待者与后续同 key 请求不会永久挂起；清理后
	// panic 继续上抛。
	doneClosed := false
	defer func() {
		if !doneClosed {
			call.entry = nil
			close(call.done)
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
		}
	}()

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
	c.mu.Lock()
	if entry.statusCode == http.StatusOK && len(entry.body) > 0 && c.gen == gen {
		c.storeEntryLocked(key, entry)
		call.entry = entry
	} else {
		call.entry = nil
	}
	if c.inflight[key] == call {
		delete(c.inflight, key)
	}
	c.mu.Unlock()
	doneClosed = true
	close(call.done)
}

func serveUsageCacheEntry(gc *gin.Context, e *usageCacheEntry) {
	gc.Data(e.statusCode, e.contentType, e.body)
}

func (c *usageResponseCache) storeEntry(key string, e *usageCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeEntryLocked(key, e)
}

func (c *usageResponseCache) storeEntryLocked(key string, e *usageCacheEntry) {
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

// canonicalUsageCacheKey 以 path + 排序后的 query 生成缓存键：参数顺序不同
// 但语义相同的请求共享同一条缓存。值经转义，防止 "b&keyName=a" 这类含分隔
// 符的筛选拼接出与多参数组合相同的键。
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
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}
