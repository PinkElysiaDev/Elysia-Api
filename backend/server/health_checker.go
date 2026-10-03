package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
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

// pruneStaleFailureKeys 清理已不存在的模型的连续失败计数：map 只增不删
// 会在长生命周期进程里随模型更名/源删除无限累积（每键一个 int）。
func (h *healthChecker) pruneStaleFailureKeys(models []storage.Model) {
	alive := make(map[string]struct{}, len(models))
	for _, model := range models {
		alive[probeKey(model.ID, model.SourceID)] = struct{}{}
	}
	h.mu.Lock()
	for key := range h.failures {
		if _, ok := alive[key]; !ok {
			delete(h.failures, key)
		}
	}
	h.mu.Unlock()
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

// probeInterval 读取当前生效的探测周期（config 层已保证 >0）。
func (h *healthChecker) probeInterval() time.Duration {
	return time.Duration(h.server.config.GetHealthCheckConfig().IntervalSeconds) * time.Second
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
	sources, err := h.server.store.ListSources(ctx)
	if err != nil {
		h.server.logWarnf("health check: failed to load current source credentials: %v", err)
		return
	}
	keyMeta := collectSourceKeys(sources)
	h.pruneStaleFailureKeys(models)

	for _, model := range models {
		ref, hasCredential := h.server.resolveModelSource(model, keyMeta)
		if !hasCredential {
			continue
		}
		model.BaseURL, model.APIKey = ref.BaseURL, ref.APIKey

		// 每次探测在 probe 内部独立限时：若整轮共享一个超时 ctx，一个慢上游
		// 就会耗尽预算，导致本轮后续所有探测连锁失败、健康模型被误禁。
		result := h.probe(context.Background(), model, cfg.TimeoutSeconds)
		if result == probeUnavailable {
			continue
		}
		if h.recordProbeResult(model, result == probeHealthy, cfg.FailureThreshold) {
			// 状态翻转立即失效路由缓存:整轮探测(串行,每模型独立超时)可达
			// 分钟级,推迟失效会让轮首被禁用的模型继续接流量。
			h.server.invalidateRouteCache()
		}
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

type healthProbeResult uint8

const (
	probeUnavailable healthProbeResult = iota
	probeHealthy
	probeUnhealthy
)

// probe uses the verified generation binding without retries. Unavailable
// probe contracts and rate limits provide no evidence to change availability.
func (h *healthChecker) probe(ctx context.Context, model storage.Model, timeoutSeconds int) healthProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	candidate, request, err := h.server.prepareHealthProbe(probeCtx, model)
	if err != nil {
		h.server.logWarnf("health check: model %s has no usable probe contract: %v", model.Name, err)
		return probeUnavailable
	}
	_, err = h.server.collectProtocolGenerationAttempt(probeCtx, candidate, request, nil, nil)
	var failure *gatewayFailure
	if errors.As(err, &failure) && failure.status == http.StatusTooManyRequests {
		return probeUnavailable
	}
	if err != nil {
		return probeUnhealthy
	}
	return probeHealthy
}

func (s *Server) prepareHealthProbe(ctx context.Context, model storage.Model) (gatewayCandidate, *protocol.Request, error) {
	service, err := s.protocolService()
	if err != nil {
		return gatewayCandidate{}, nil, err
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return gatewayCandidate{}, nil, err
	}
	ref := config.ModelRef{ID: model.ID, Name: model.Name, SourceID: model.SourceID, BaseURL: model.BaseURL, APIKey: model.APIKey}
	view := service.View()
	var candidate gatewayCandidate
	var failure *protocol.ConversionError
	for _, transport := range []protocol.Transport{protocol.HTTPJSON, protocol.SSE, protocol.NDJSON} {
		candidate, failure = makeGatewayCandidate(view, bindings, ref, transport, "generate")
		if failure == nil {
			break
		}
	}
	if failure != nil {
		return candidate, nil, failure
	}
	if candidate.operation.Kind != "generate" {
		return candidate, nil, gatewayIssue(candidate.compiled.Identity(), protocol.UnsupportedCapability, "/operations", "background health probes require synchronous generation")
	}
	budget, err := protocol.EncodeValue(HealthProbeMaxTokens)
	if err != nil {
		return candidate, nil, err
	}
	stream, err := protocol.EncodeValue(candidate.operation.Transport != protocol.HTTPJSON)
	if err != nil {
		return candidate, nil, err
	}
	request := &protocol.Request{
		SchemaVersion: protocol.SemanticSchemaVersion,
		Source:        protocol.Identity{Family: "elysia-health", WireVersion: "1"},
		Model:         protocol.StringValue(model.Name),
		Content:       []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("ping")}}}},
		Parameters:    protocol.Object{"max_output_tokens": budget, "stream": stream},
	}
	if err := protocol.IssuesError(protocol.CheckRoute(request, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); err != nil {
		return candidate, nil, err
	}
	body, err := candidate.compiled.EncodeRequest(ctx, request, protocol.EvaluationContext{Scope: candidate.scope})
	if err != nil {
		return candidate, nil, err
	}
	return candidate, request, candidate.compiled.CheckOperationInput(candidate.operation, body)
}
