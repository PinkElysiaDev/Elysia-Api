package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// relayAttemptStep 是单次尝试的执行结果：要么携带转发 outcome（可能已提交），
// 要么带着可重试的构造期失败信息跳过该候选（skipErr 非 nil）。
type relayAttemptStep struct {
	outcome    relayOutcome
	skipErr    error
	skipStatus int
	skipClass  relay.ErrorClass
}

// runRelayAttempts 是 chatCompletions 与 responses 共用的故障转移骨架：
// 循环顶部取消拦截 → SSRF 出站校验 → 单次尝试（run 回调）→ 失败记账 →
// 重试等待 → 全部失败后的兜底提交。failureBody 渲染两入口各自的错误体
// 形态（扁平 gin.H vs OpenAI typed 对象）；run 内的构造期失败以 skipErr
// 返回，由骨架统一走 appendRetryEvent / 末次 commitLastAttemptFailure。
func (s *Server) runRelayAttempts(
	c *gin.Context,
	record *usageRecord,
	startTime time.Time,
	group *config.ModelGroupConfig,
	candidates []config.ModelRef,
	format relay.FormatType,
	run func(attempt int, selectedModel config.ModelRef, isLast bool) relayAttemptStep,
) {
	attempts := maxAttempts(group.MaxRetries, len(candidates))
	var lastStatus int
	var lastErr string
	committed := false

	for attempt := 0; attempt < attempts; attempt++ {
		// 循环顶部拦截客户端取消：interval=0 时无等待期可拦截，断连后
		// 仍会向剩余候选逐个扇出空耗上游配额。
		if attempt > 0 && s.abortRetryOnClientCancel(c, record, startTime) {
			committed = true
			return
		}
		selectedModel := candidates[attempt]
		isLast := attempt == attempts-1

		// SSRF 出站校验。校验失败属于配置/安全问题，对单个候选不可恢复，
		// 但其他候选可能合法，因此记为可重试。
		if err := s.validateOutbound(selectedModel.BaseURL); err != nil {
			lastStatus = http.StatusForbidden
			lastErr = fmt.Sprintf("target baseUrl rejected: %v", err)
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				s.commitLastAttemptFailure(c, record, startTime, format, &relay.MaheshvaraError{Class: relay.ErrorClassPermission, Status: lastStatus, Message: lastErr})
				committed = true
			}
			continue
		}

		step := run(attempt, selectedModel, isLast)
		if step.skipErr != nil {
			lastStatus = step.skipStatus
			lastErr = step.skipErr.Error()
			s.appendRetryEvent(record, attempt, selectedModel.Name, lastErr)
			if isLast {
				s.commitLastAttemptFailure(c, record, startTime, format, &relay.MaheshvaraError{Class: step.skipClass.OrDefault(), Status: lastStatus, Message: lastErr})
				committed = true
			}
			continue
		}

		outcome := step.outcome
		if outcome.committed {
			committed = true
			// 成功（2xx）时记录渠道亲和性，让后续同 key+group 请求优先复用本模型。
			if outcome.statusCode >= 200 && outcome.statusCode < 300 {
				// TTL 基准用完成时刻而非请求开始:长流式(>=TTL)以 startTime 计算的
				// 粘连写入即过期,上游 prompt 缓存收益最大的场景反而拿不到粘连。
				s.affinity.set(record.KeyHash, group.ID, selectedModel.Name, time.Now())
			}
			break
		}

		// 未提交：本次失败但可重试。记录失败原因，等待重试间隔后换下一个候选。
		lastStatus = outcome.statusCode
		lastErr = outcome.errMsg
		s.appendRetryEvent(record, attempt, selectedModel.Name, outcome.errMsg)
		if !isLast && group.RetryInterval > 0 {
			// 尊重客户端取消：被放弃的请求不再空耗等待 + 对剩余候选扇出
			//（取消同样落库留痕，499 为 nginx 惯例的 client closed）。
			if !waitForRetryOrCancel(c, group.RetryInterval) {
				committed = true
				s.abortRetryOnClientCancel(c, record, startTime)
				return
			}
		}
	}

	// 兜底：最后一次尝试一定会 commit（failResult 的 isLast||!retryable 分支
	// 与全部提前返回已覆盖）；此块仅防御未来路径回归。lastStatus 理论上
	// 必非 0，但真为 0 时 c.JSON(0,…) 会让 net/http panic 且记录丢失——
	// 兜底的兜底，一行守卫换掉一个潜在 panic。先写响应再落记录：错误体进
	// 下游捕获器后，第四段才有内容；状态码与记录保持一致（旧实现记录
	// 429 却恒回 502）。
	if !committed {
		if lastStatus <= 0 {
			lastStatus = http.StatusBadGateway
		}
		record.StatusCode = lastStatus
		record.Error = firstNonEmpty(lastErr, "all upstream attempts failed")
		record.ErrorKind = ErrorKindUpstream
		record.EndedAt = time.Now()
		record.DurationMs = time.Since(startTime).Milliseconds()
		writeProtocolError(c, format, &relay.MaheshvaraError{Class: relay.ErrorClassUpstream, Status: lastStatus, Message: record.Error})
		s.recordUsage(record)
	}
}
