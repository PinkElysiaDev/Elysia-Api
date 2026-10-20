package server

import (
	"context"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/routing"
	"github.com/elysia-api/backend/storage"
	"github.com/rs/zerolog"
)

// healthChecker 是可选的后台模型健康检测器（默认关闭，由 config.HealthCheck.Enabled 控制）。
// 它周期性地对每个模型发一个廉价探测请求：
//   - 连续失败达到阈值 → 自动禁用（models.available=0），活跃流量立即不再路由过去；
//   - 已禁用模型仍持续探测，一旦成功 → 自动重新启用。
//
// 状态变更后失效路由缓存，使转发热路径立即感知。所有探测都经过 SSRF 出站校验。
type healthChecker struct {
	store      healthRepository
	settings   func() config.HealthCheckConfig
	logEvent   func(string) *zerolog.Event
	probe      func(context.Context, storage.Model, int) healthProbeResult
	invalidate func()

	mu       sync.Mutex
	failures map[string]int // key: modelID\x00sourceID → 连续失败次数

	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	startOnce sync.Once
}

type healthRepository interface {
	ListAllModelsForProbe(context.Context) ([]storage.Model, error)
	ListSources(context.Context) ([]storage.ModelSource, error)
	SetModelAvailability(context.Context, string, string, bool) (int64, error)
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

func probeKey(modelID, sourceID string) string { return modelID + nulSeparator + sourceID }

// start 在 store 可用时启动后台探测循环。enabled 与 interval 每轮从配置
// 热读取：旧实现把 interval 烘死在 ticker 里、enabled 只在启动时看一眼，
// 热重载改配置完全无效。禁用状态循环保持空转（每周期一次 timer 唤醒，
// 代价可忽略），重新启用无需重启进程。
func (h *healthChecker) start() {
	h.startOnce.Do(func() {
		if h.ctx.Err() != nil {
			close(h.done)
			return
		}
		if cfg := h.settings(); cfg.Enabled {
			interval := h.probeInterval()
			h.logEvent("info").Str("event", "health_checker_enabled").Dur("interval_ms", interval).Int("timeout_seconds", cfg.TimeoutSeconds).Int("failure_threshold", cfg.FailureThreshold).Msg("health checker enabled")
		}
		go func() {
			defer close(h.done)
			interval := h.probeInterval()
			timer := time.NewTimer(interval)
			defer timer.Stop()
			if h.settings().Enabled {
				// 启动后先跑一轮，不必等第一个 interval。
				h.runOnce()
			}
			for {
				select {
				case <-h.ctx.Done():
					return
				case <-timer.C:
					if h.settings().Enabled {
						h.runOnce()
					}
					// 周期热更新：interval 变化从下一轮生效。
					timer.Reset(h.probeInterval())
				}
			}
		}()
	})
}

// probeInterval 读取当前生效的探测周期（config 层已保证 >0）。
func (h *healthChecker) probeInterval() time.Duration {
	return time.Duration(h.settings().IntervalSeconds) * time.Second
}

func (h *healthChecker) shutdown() {
	h.cancel()
	h.start()
	<-h.done
}

// runOnce 探测一轮所有模型。
func (h *healthChecker) runOnce() {
	cfg := h.settings()
	ctx, cancel := context.WithTimeout(h.ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	models, err := h.store.ListAllModelsForProbe(ctx)
	if err != nil {
		if h.ctx.Err() == nil {
			h.logEvent("warn").Err(err).Msg("health check: failed to list models")
		}
		return
	}
	sources, err := h.store.ListSources(ctx)
	if err != nil {
		h.logEvent("warn").Err(err).Msg("health check: failed to load current source credentials")
		return
	}
	keyMeta := routing.IndexSources(sources)
	h.pruneStaleFailureKeys(models)

	for _, model := range models {
		if h.ctx.Err() != nil {
			return
		}
		ref, hasCredential := keyMeta.Resolve(model)
		if !hasCredential {
			continue
		}
		model.BaseURL, model.APIKey = ref.BaseURL, ref.APIKey

		// 探测父 ctx 取 h.ctx（仅可取消、无 deadline）：既保持每探测独立限时
		// （慢上游只消耗自己的预算），又让关停取消能中断在途探测。
		result := h.probe(h.ctx, model, cfg.TimeoutSeconds)
		// 停机取消不是上游故障，不计失败或更改模型可用性。
		if h.ctx.Err() != nil {
			return
		}
		if result == probeUnavailable {
			continue
		}
		if h.recordProbeResult(model, result == probeHealthy, cfg.FailureThreshold) {
			// 状态翻转立即失效路由缓存:整轮探测(串行,每模型独立超时)可达
			// 分钟级,推迟失效会让轮首被禁用的模型继续接流量。
			h.invalidate()
		}
	}
}

// record 根据探测结果更新连续失败计数，并在跨过阈值时切换 available 状态。
// 返回 true 表示发生了状态变更。
func (h *healthChecker) recordProbeResult(model storage.Model, ok bool, threshold int) bool {
	if h.ctx.Err() != nil {
		return false
	}
	key := probeKey(model.ID, model.SourceID)
	h.mu.Lock()
	if ok {
		h.failures[key] = 0
	} else {
		h.failures[key]++
	}
	failCount := h.failures[key]
	h.mu.Unlock()

	ctx := h.ctx
	switch {
	case ok && !model.Available:
		// 恢复：探测成功且当前被禁用 → 重新启用
		if _, err := h.store.SetModelAvailability(ctx, model.ID, model.SourceID, true); err != nil {
			h.logEvent("warn").Str("model_id", model.ID).Str("source_id", model.SourceID).Err(err).Msg("health check: failed to re-enable model")
			return false
		}
		h.logEvent("info").Str("model_name", model.Name).Str("source_id", model.SourceID).Msg("health check: model recovered and re-enabled")
		return true
	case !ok && model.Available && failCount >= threshold:
		// 禁用：连续失败达到阈值且当前可用 → 禁用
		if _, err := h.store.SetModelAvailability(ctx, model.ID, model.SourceID, false); err != nil {
			h.logEvent("warn").Str("model_id", model.ID).Str("source_id", model.SourceID).Err(err).Msg("health check: failed to disable model")
			return false
		}
		h.logEvent("warn").Str("model_name", model.Name).Str("source_id", model.SourceID).Int("consecutive_failures", failCount).Msg("health check: model auto-disabled")
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
