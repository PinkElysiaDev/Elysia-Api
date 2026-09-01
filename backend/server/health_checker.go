package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// healthChecker 是可选的后台模型健康检测器（默认关闭，由 config.HealthCheck.Enabled 控制）。
// 它周期性地对每个模型发一个廉价探测请求：
//   - 连续失败达到阈值 → 自动禁用（models.available=0），活跃流量立即不再路由过去；
//   - 已禁用模型仍持续探测，一旦成功 → 自动重新启用。
//
// 状态变更后失效路由缓存，使转发热路径立即感知。所有探测都经过 SSRF 出站校验。
type healthChecker struct {
	server *Server

	mu       sync.Mutex
	failures map[string]int // key: modelID\x00sourceID → 连续失败次数

	// client 全 checker 共享（见 newHealthChecker 注释）。
	client *http.Client

	stop chan struct{}
	done chan struct{}
}

func newHealthChecker(s *Server) *healthChecker {
	return &healthChecker{
		server: s,
		// client 全 checker 共享：此前每次探测新建 Transport，空闲连接只能等
		// GC finalizer 回收——几百模型×每 300s 一轮会持续制造 socket/FD churn。
		// 探测走与转发路径相同的 SSRF 防护 Transport（连接时校验每个实际拨号
		// IP，含重定向后的目标）；超时由每次探测的 ctx 控制。
		client:   &http.Client{Transport: relay.NewSecureTransport()},
		failures: make(map[string]int),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func probeKey(modelID, sourceID string) string { return modelID + "\x00" + sourceID }

// start 在 store 可用时启动后台探测循环。enabled 与 interval 每轮从配置
// 热读取：旧实现把 interval 烘死在 ticker 里、enabled 只在启动时看一眼，
// 热重载改配置完全无效。禁用状态循环保持空转（每周期一次 timer 唤醒，
// 代价可忽略），重新启用无需重启进程。
func (h *healthChecker) start() {
	if h.server.store == nil {
		close(h.done)
		return
	}
	if cfg := h.server.config.GetHealthCheckConfig(); cfg.Enabled {
		interval := h.probeInterval()
		h.server.logInfof("health checker enabled: interval=%s timeout=%ds failureThreshold=%d", interval, cfg.TimeoutSeconds, cfg.FailureThreshold)
	}
	go func() {
		defer close(h.done)
		interval := h.probeInterval()
		timer := time.NewTimer(interval)
		defer timer.Stop()
		if h.server.config.GetHealthCheckConfig().Enabled {
			// 启动后先跑一轮，不必等第一个 interval。
			h.runOnce()
		}
		for {
			select {
			case <-h.stop:
				return
			case <-timer.C:
				if h.server.config.GetHealthCheckConfig().Enabled {
					h.runOnce()
				}
				// 周期热更新：interval 变化从下一轮生效。
				timer.Reset(h.probeInterval())
			}
		}
	}()
}

// probeInterval 读取当前生效的探测周期（非法值回落默认 300s）。
func (h *healthChecker) probeInterval() time.Duration {
	cfg := h.server.config.GetHealthCheckConfig()
	if cfg.IntervalSeconds <= 0 {
		return 300 * time.Second
	}
	return time.Duration(cfg.IntervalSeconds) * time.Second
}

func (h *healthChecker) shutdown() {
	select {
	case <-h.stop:
		// already closed
	default:
		close(h.stop)
	}
	<-h.done
	h.client.CloseIdleConnections()
}

// runOnce 探测一轮所有模型。
func (h *healthChecker) runOnce() {
	cfg := h.server.config.GetHealthCheckConfig()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	models, err := h.server.store.ListAllModelsForProbe(ctx)
	if err != nil {
		h.server.logWarnf("health check: failed to list models: %v", err)
		return
	}

	changed := false
	for _, model := range models {
		// 每次探测在 probe 内部独立限时：若整轮共享一个超时 ctx，一个慢上游
		// 就会耗尽预算，导致本轮后续所有探测连锁失败、健康模型被误禁。
		ok := h.probe(context.Background(), model, cfg.TimeoutSeconds)
		if h.recordProbeResult(model, ok, cfg.FailureThreshold) {
			changed = true
		}
	}
	if changed {
		// 可用性变更后失效路由缓存，让转发立即感知。
		h.server.invalidateRouteCache()
	}
}

// record 根据探测结果更新连续失败计数，并在跨过阈值时切换 available 状态。
// 返回 true 表示发生了状态变更。
func (h *healthChecker) recordProbeResult(model storage.Model, ok bool, threshold int) bool {
	key := probeKey(model.ID, model.SourceID)
	h.mu.Lock()
	if ok {
		h.failures[key] = 0
	} else {
		h.failures[key]++
	}
	failCount := h.failures[key]
	h.mu.Unlock()

	ctx := context.Background()
	switch {
	case ok && !model.Available:
		// 恢复：探测成功且当前被禁用 → 重新启用
		if _, err := h.server.store.SetModelAvailability(ctx, model.ID, model.SourceID, true); err != nil {
			h.server.logWarnf("health check: failed to re-enable model %s: %v", model.ID, err)
			return false
		}
		h.server.logInfof("health check: model %s (source %s) recovered, re-enabled", model.Name, model.SourceID)
		return true
	case !ok && model.Available && failCount >= threshold:
		// 禁用：连续失败达到阈值且当前可用 → 禁用
		if _, err := h.server.store.SetModelAvailability(ctx, model.ID, model.SourceID, false); err != nil {
			h.server.logWarnf("health check: failed to disable model %s: %v", model.ID, err)
			return false
		}
		h.server.logWarnf("health check: model %s (source %s) failed %d consecutive probes, auto-disabled", model.Name, model.SourceID, failCount)
		return true
	}
	return false
}

// probe 对单个模型发一个最小探测请求，返回是否健康。
// 使用各平台原生的轻量端点；任何 2xx 视为健康。探测请求经 SSRF 校验。
func (h *healthChecker) probe(ctx context.Context, model storage.Model, timeoutSeconds int) bool {
	if err := h.server.validateOutbound(model.BaseURL); err != nil {
		return false
	}
	// 单次探测独立超时，避免上一个慢探测挤占本轮预算。
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, probeEndpoint(model), bytes.NewReader(probeBody(model)))
	if err != nil {
		return false
	}
	applyProbeAuth(req, model)
	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 2xx = 健康。401/403（鉴权失败）也应视为不健康并禁用。
	// 429（限流）说明上游其实活着，视为健康，避免误禁。
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	// 404/405：上游可达且正常响应，只是该端点不支持探测（如网关只实现了
	// chat/completions）。鉴权与连通性已验证，不计入失败以免误禁。
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return true
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// probeBody 按归一化后的线路协议构造最小探测请求体。
func probeBody(model storage.Model) []byte {
	switch relay.NormalizeAPIFormat(model.Platform) {
	case relay.APIFormatGemini:
		return []byte(`{"contents":[{"parts":[{"text":"ping"}]}],"generationConfig":{"maxOutputTokens":1}}`)
	case relay.APIFormatResponses:
		return []byte(fmt.Sprintf(`{"model":%q,"input":"ping","max_output_tokens":%d}`, model.Name, HealthProbeMaxTokens))
	default:
		return []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":%d}`, model.Name, HealthProbeMaxTokens))
	}
}

func probeEndpoint(model storage.Model) string {
	base := model.BaseURL
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	switch relay.NormalizeAPIFormat(model.Platform) {
	case relay.APIFormatAnthropic:
		// baseUrl 不含 /v1（与 fetchClaudeModels / ClaudeAdapter 一致），探测必须打 /v1/messages。
		return base + "/v1/messages"
	case relay.APIFormatGemini:
		// 与 relay.GeminiAdapter 的 URL 规则保持一致。
		return base + "/v1beta/models/" + model.Name + ":generateContent"
	case relay.APIFormatResponses:
		return base + "/responses"
	default:
		return base + "/chat/completions"
	}
}

func applyProbeAuth(req *http.Request, model storage.Model) {
	if model.APIKey == "" {
		return
	}
	switch relay.NormalizeAPIFormat(model.Platform) {
	case relay.APIFormatAnthropic:
		req.Header.Set("x-api-key", model.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case relay.APIFormatGemini:
		req.Header.Set("x-goog-api-key", model.APIKey)
	default:
		req.Header.Set("Authorization", "Bearer "+model.APIKey)
	}
}
