package relay

import (
	"net/http"
	"sync"
	"time"
)

// dynamicTimeoutClient 封装可运行时替换的 *http.Client。
// admin 面板修改 httpTimeout 后通过 SetTimeout 即时生效，无需重启进程
// （http.Client.Timeout 在构造时固化，运行中原地改写字段存在数据竞争）。
// Transport 全程共享（http.Transport 并发安全），仅整体替换 Client 指针；
// 热路径读取走 RLock，开销可忽略。流式路径不设 Timeout，不走此类型。
type dynamicTimeoutClient struct {
	mu        sync.RWMutex
	client    *http.Client
	transport http.RoundTripper
}

func newDynamicTimeoutClient(timeout time.Duration) *dynamicTimeoutClient {
	transport := newSecureTransport()
	client := &http.Client{Transport: transport}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return &dynamicTimeoutClient{client: client, transport: transport}
}

// Do 用当前生效的 client 发送请求。
func (c *dynamicTimeoutClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()
	return client.Do(req)
}

// SetTimeout 以新超时整体替换 client；d <= 0 表示不限制。
func (c *dynamicTimeoutClient) SetTimeout(d time.Duration) {
	client := &http.Client{Transport: c.transport}
	if d > 0 {
		client.Timeout = d
	}
	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
}
