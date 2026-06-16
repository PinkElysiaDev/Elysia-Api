package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

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

	stop chan struct{}
	done chan struct{}
}

func newHealthChecker(s *Server) *healthChecker {
	return &healthChecker{
		server:   s,
		failures: make(map[string]int),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func probeKey(modelID, sourceID string) string { return modelID + "\x00" + sourceID }

// start 在配置启用且 store 可用时启动后台探测循环。
func (h *healthChecker) start() {
	cfg := h.server.config.GetHealthCheckConfig()
	if !cfg.Enabled || h.server.store == nil {
		close(h.done)
		return
	}
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	h.server.logInfof("health checker enabled: interval=%s timeout=%ds failureThreshold=%d", interval, cfg.TimeoutSeconds, cfg.FailureThreshold)
	go func() {
		defer close(h.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// 启动后先跑一轮，不必等第一个 interval。
		h.runOnce()
		for {
			select {
			case <-h.stop:
				return
			case <-ticker.C:
				h.runOnce()
			}
		}
	}()
}

func (h *healthChecker) shutdown() {
	select {
	case <-h.stop:
		// already closed
	default:
		close(h.stop)
	}
	<-h.done
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
		ok := h.probe(ctx, model, cfg.TimeoutSeconds)
		if h.record(model, ok, cfg.FailureThreshold) {
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
func (h *healthChecker) record(model storage.Model, ok bool, threshold int) bool {
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
	// 用一个极小的 chat/completions 请求做探测：max_tokens=1，单字提示。
	// 对绝大多数 OpenAI 兼容/Claude/Gemini 上游都能触发一次真实鉴权+路由。
	body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":%d}`, model.Name, HealthProbeMaxTokens))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, probeEndpoint(model), bytes.NewReader(body))
	if err != nil {
		return false
	}
	applyProbeAuth(req, model)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 2xx = 健康。401/403（鉴权失败）也应视为不健康并禁用。
	// 429（限流）说明上游其实活着，视为健康，避免误禁。
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func probeEndpoint(model storage.Model) string {
	base := model.BaseURL
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	switch model.Platform {
	case "claude":
		return base + "/messages"
	default:
		return base + "/chat/completions"
	}
}

func applyProbeAuth(req *http.Request, model storage.Model) {
	if model.APIKey == "" {
		return
	}
	switch model.Platform {
	case "claude":
		req.Header.Set("x-api-key", model.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+model.APIKey)
	}
}
