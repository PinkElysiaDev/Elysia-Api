package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func parseTestIP(t *testing.T, raw string) net.IP {
	t.Helper()
	ip := net.ParseIP(raw)
	if ip == nil {
		t.Fatalf("invalid test IP: %s", raw)
	}
	return ip
}

// newTestServer 构造一个不依赖 SQLite 的 Server（store=nil → usage 走内存切片，
// groups 走 config）。跳过 SSRF 校验以便上游指向 httptest 的 127.0.0.1。
func newTestServer(groups []config.ModelGroupConfig) *Server {
	gin.SetMode(gin.TestMode)
	// 上游指向 httptest 的 127.0.0.1：关闭 relay 的连接时 SSRF 校验，
	// 否则私网 IP 会被 secureControl 在 connect 时拒绝。
	relay.SetAllowPrivateDial(true)
	cfg := &config.Config{}
	cfg.Groups = groups
	cfg.Server = config.ServerConfig{Host: "127.0.0.1", Port: 8765}
	return &Server{
		config:                 cfg,
		engine:                 gin.New(),
		openaiAdapter:          relay.NewOpenAIAdapter(10 * time.Second),
		claudeAdapter:          relay.NewClaudeAdapter(10 * time.Second),
		geminiAdapter:          relay.NewGeminiAdapter(10 * time.Second),
		roundRobinIndex:        make(map[string]int),
		rateLimits:             make(map[string]*rateLimitState),
		affinity:               newAffinityCache(),
		skipOutboundValidation: true,
	}
}

func openAIModel(name, baseURL string) config.ModelRef {
	return config.ModelRef{ID: name, Name: name, BaseURL: baseURL, Platform: "openai", APIKey: "test-key"}
}

func chatRequestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}

func okChatCompletionBody(t *testing.T) string {
	t.Helper()
	resp := relay.OpenAIResponse{
		ID:      "cmpl-1",
		Object:  "chat.completion",
		Model:   "upstream",
		Choices: []relay.Choice{{Index: 0, Message: relay.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"}},
		Usage:   relay.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal upstream response: %v", err)
	}
	return string(data)
}

// 核心回归：故障转移。首个候选返回 500（可重试），第二个返回 200，
// 客户端应得到 200，且第二个上游被实际调用。
func TestChatCompletionsFailoverToHealthyModel(t *testing.T) {
	var firstHits, secondHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"upstream boom"}`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okChatCompletionBody(t))
	}))
	defer good.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 2,
		Models: []config.ModelRef{openAIModel("m-bad", bad.URL), openAIModel("m-good", good.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hello"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after failover, got %d body=%s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&firstHits) != 1 {
		t.Fatalf("first (bad) upstream should be hit once, got %d", firstHits)
	}
	if atomic.LoadInt32(&secondHits) != 1 {
		t.Fatalf("second (good) upstream should be hit once, got %d", secondHits)
	}
}

// 不可重试状态码（400）不应触发故障转移：第二个上游不应被调用，
// 客户端直接收到 400。
func TestChatCompletionsNoRetryOnClientError(t *testing.T) {
	var firstHits, secondHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer bad.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		_, _ = io.WriteString(w, okChatCompletionBody(t))
	}))
	defer second.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 3,
		Models: []config.ModelRef{openAIModel("m-bad", bad.URL), openAIModel("m-2", second.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 passthrough, got %d", rec.Code)
	}
	if atomic.LoadInt32(&firstHits) != 1 {
		t.Fatalf("first upstream should be hit once, got %d", firstHits)
	}
	if atomic.LoadInt32(&secondHits) != 0 {
		t.Fatalf("second upstream must NOT be tried on non-retryable error, got %d", secondHits)
	}
}

// 所有候选都失败时，客户端应收到最后一次的错误状态码，且每个候选都被尝试。
func TestChatCompletionsAllModelsFail(t *testing.T) {
	var hits int32
	makeBad := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":"down"}`)
		}))
	}
	a, b := makeBad(), makeBad()
	defer a.Close()
	defer b.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 5,
		Models: []config.ModelRef{openAIModel("a", a.URL), openAIModel("b", b.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when all fail, got %d", rec.Code)
	}
	// MaxRetries=5 但只有 2 个候选 → 最多尝试 2 次
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected 2 upstream attempts (capped by candidate count), got %d", hits)
	}
}

// 验证 maxRetries=0（不重试）时，失败就直接返回，不尝试第二个候选。
func TestChatCompletionsMaxRetriesZero(t *testing.T) {
	var firstHits, secondHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		_, _ = io.WriteString(w, okChatCompletionBody(t))
	}))
	defer good.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 0,
		Models: []config.ModelRef{openAIModel("bad", bad.URL), openAIModel("good", good.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if atomic.LoadInt32(&firstHits) != 1 || atomic.LoadInt32(&secondHits) != 0 {
		t.Fatalf("maxRetries=0 should try exactly 1 model: first=%d second=%d", firstHits, secondHits)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestChatCompletionsDisabledGroup(t *testing.T) {
	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: false, Strategy: "sequential",
		Models: []config.ModelRef{openAIModel("m", "http://example.com")},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled group should return 403, got %d", rec.Code)
	}
}

func TestChatCompletionsUnknownGroup(t *testing.T) {
	s := newTestServer(nil)
	c, rec := chatRequestContext(`{"model":"nope","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown group should return 404, got %d", rec.Code)
	}
}

// 限流：超出 MaxConcurrency 时返回 429。这里通过把活跃数顶满来验证 acquire 逻辑。
func TestAcquireRateLimitConcurrency(t *testing.T) {
	s := newTestServer(nil)
	group := &config.ModelGroupConfig{ID: "g1", Name: "grp", MaxConcurrency: 1}

	release1, err := s.acquireRateLimit(group, 0)
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	if _, err := s.acquireRateLimit(group, 0); err == nil {
		t.Fatalf("second acquire should be rejected by max concurrency")
	}
	release1()
	if _, err := s.acquireRateLimit(group, 0); err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
}

func TestAcquireRateLimitDailyRequests(t *testing.T) {
	s := newTestServer(nil)
	group := &config.ModelGroupConfig{ID: "g1", Name: "grp", DailyLimitMaxRequests: 2}
	for i := 0; i < 2; i++ {
		release, err := s.acquireRateLimit(group, 0)
		if err != nil {
			t.Fatalf("acquire %d should succeed: %v", i, err)
		}
		release()
	}
	if _, err := s.acquireRateLimit(group, 0); err == nil {
		t.Fatalf("daily request limit should reject the 3rd request")
	}
}

// H1 回归：失败请求（只 acquire+release、从不 adjustTokenUsage）必须把预留的
// estimatedTokens 如数退还，不能永久占用每日 token 配额。
func TestAcquireRateLimitRefundsReservationOnRelease(t *testing.T) {
	s := newTestServer(nil)
	group := &config.ModelGroupConfig{ID: "g1", Name: "grp", DailyLimitMaxTokens: 1000}

	// 预留 800，随后 release（模拟请求失败：不调用 adjustTokenUsage）。
	release, err := s.acquireRateLimit(group, 800)
	if err != nil {
		t.Fatalf("acquire should succeed: %v", err)
	}
	release()

	// 退还后每日 token 计数应回到 0，新的 800 预留仍应被接受。
	if got := s.rateLimits["g1"].Tokens; got != 0 {
		t.Fatalf("reservation must be refunded on release, got Tokens=%d", got)
	}
	if _, err := s.acquireRateLimit(group, 800); err != nil {
		t.Fatalf("after refund a fresh 800-token reservation should fit under 1000: %v", err)
	}
}

// H1 成功路径：release 退还预留、adjustTokenUsage 累加实际值，净额应为实际消耗。
func TestRateLimitSettlesToActualOnSuccess(t *testing.T) {
	s := newTestServer(nil)
	group := &config.ModelGroupConfig{ID: "g1", Name: "grp", DailyLimitMaxTokens: 10000}

	release, err := s.acquireRateLimit(group, 800) // 预留估算值
	if err != nil {
		t.Fatalf("acquire should succeed: %v", err)
	}
	s.adjustTokenUsage("g1", 320) // 拿到实际值后累加
	release()                     // 退还预留

	if got := s.rateLimits["g1"].Tokens; got != 320 {
		t.Fatalf("daily tokens should net to actual usage 320, got %d", got)
	}
}

func TestExtractAccessToken(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(r *http.Request)
		expect string
	}{
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer abc123") }, "abc123"},
		{"x-api-key", func(r *http.Request) { r.Header.Set("x-api-key", "key789") }, "key789"},
		{"x-goog", func(r *http.Request) { r.Header.Set("x-goog-api-key", "goog1") }, "goog1"},
		{"query", func(r *http.Request) { r.URL.RawQuery = "key=qk" }, "qk"},
		{"none", func(r *http.Request) {}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			tc.setup(r)
			if got := extractAccessToken(r); got != tc.expect {
				t.Fatalf("extractAccessToken = %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestValidateOutboundBaseURLRejectsLoopbackAndPrivate(t *testing.T) {
	rejects := []string{
		"http://localhost/v1",
		"http://127.0.0.1/v1",
		"http://10.0.0.1/v1",
		"http://192.168.1.1/v1",
		"http://169.254.169.254/v1", // 云元数据
		"ftp://example.com/v1",      // 非 http(s)
		"http://user:pass@example.com/v1",
	}
	for _, raw := range rejects {
		if err := validateOutboundBaseURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected by SSRF guard", raw)
		}
	}
}

func TestIsPrivateOrRestrictedIP(t *testing.T) {
	// 含 RFC1918/环回/链路本地/CGNAT/0.0.0.0 以及保留文档与基准测试段
	// （192.0.2/24、198.18/15、198.51.100/24、203.0.113/24、240/4）——
	// 这些都不可路由，作为上游应一律拒绝（H3 收紧后的行为）。
	private := []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1", "169.254.0.1", "100.64.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1", "192.0.2.10", "198.18.0.1", "198.51.100.5", "203.0.113.10", "240.0.0.1"}
	public := []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "2606:4700:4700::1111"}
	for _, ip := range private {
		if !isPrivateOrRestrictedIP(parseTestIP(t, ip)) {
			t.Fatalf("%s should be private/restricted", ip)
		}
	}
	for _, ip := range public {
		if isPrivateOrRestrictedIP(parseTestIP(t, ip)) {
			t.Fatalf("%s should be considered public", ip)
		}
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5000":  true,
		"[::1]:5000":      true,
		"10.0.0.5:5000":   false,
		"203.0.113.1:443": false,
	}
	for remote, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/__reload", nil)
		r.RemoteAddr = remote
		if got := isLoopbackRequest(r); got != want {
			t.Fatalf("isLoopbackRequest(%q) = %v, want %v", remote, got, want)
		}
	}
}

func ExampleServer_smoke() {
	fmt.Println("ok")
	// Output: ok
}

// 流式请求：上游返回 200 但 body 立即关闭（空 SSE 流），转发会出错。
// 回归断言：record.StatusCode 必须从 200 下调为失败码，否则会被日志/统计误判为成功。
func TestStreamEmptyUpstreamRecordsFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// 不写任何 SSE 事件，直接结束 —— 模拟"上游返回结构体为空"。
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 1,
		Models: []config.ModelRef{openAIModel("m", upstream.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	s.chatCompletions(c)

	records := s.usageSnapshot()
	if len(records) == 0 {
		t.Fatal("expected a usage record to be written")
	}
	last := records[len(records)-1]
	if last.StatusCode < 400 {
		t.Fatalf("empty/failed upstream stream must NOT be recorded as success, got statusCode=%d error=%q", last.StatusCode, last.Error)
	}
}
