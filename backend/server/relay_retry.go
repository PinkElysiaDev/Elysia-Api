package server

import (
	"math/rand"

	"github.com/elysia-api/backend/config"
)

// relayOutcome 是单次转发尝试的结果，供故障转移循环决策。
//   - committed=true: 已向客户端写出响应（成功，或已是最后一次/不可重试的失败），
//     循环必须停止。
//   - committed=false: 本次失败且可以重试，循环应尝试下一个候选模型。
//     statusCode/errMsg 记录失败信息，用于日志与最终兜底响应。
type relayOutcome struct {
	committed  bool
	statusCode int
	errMsg     string
}

// appendRetryEvent 把一次失败尝试追加到 usage 记录的 RetryEvents，并更新 RetryCount。
// attempt 从 0 计数（0 即首次尝试，不算重试）。RetryCount 取「本次失败之前已发生的
// 重试次数」= attempt 本身：首次尝试失败 → 0 次重试；attempt=2 失败 → 已重试 2 次。
// 各次调用的 attempt 单调递增，故末次失败写入的值即最终重试次数（修正旧的 off-by-one）。
func (s *Server) appendRetryEvent(record *usageRecord, attempt int, model, errMsg string) {
	if record == nil {
		return
	}
	if attempt > record.RetryCount {
		record.RetryCount = attempt
	}
	record.RetryEvents = append(record.RetryEvents, retryEvent{
		Attempt: attempt,
		Model:   model,
		Error:   truncateRetryError(errMsg),
	})
}

// truncateRetryError 限制单条重试错误的长度，避免上游返回的大块错误体
// 撑爆 usage 记录。
func truncateRetryError(msg string) string {
	if len(msg) > RetryErrorMaxLen {
		return msg[:RetryErrorMaxLen] + "...(truncated)"
	}
	return msg
}

// retryEvent 记录单次失败尝试，用于写入 usage 的 RetryEvents。
// 字段定义见 usage.go 的 retryEvent 类型。

// orderedCandidates 根据模型组策略返回**完整的、有序的**候选模型列表，
// 供故障转移逐个尝试。这取代了旧的 selectModel：旧实现只返回单个模型，
// 且 round-robin 在并发下用过期索引访问 models[idx] 会越界 panic。
//
// 返回的切片长度始终等于 len(group.Models)（去重前），第 0 个元素是
// 本次请求"首选"模型，后续元素是按策略排列的备选模型：
//   - round-robin: 从原子推进的游标处开始，环绕一圈
//   - random:      随机起点，环绕一圈（等价于随机打乱的旋转）
//   - sequential:  原始顺序（models[0], models[1], ...）
//   - default:     原始顺序
//
// rrIndex 仅用于 round-robin：传入当前游标值，返回应作为起点的下标。
// 调用方负责在持有 roundRobinMutex 时推进游标。
func orderedCandidates(group *config.ModelGroupConfig, rrStart int) []config.ModelRef {
	models := group.Models
	n := len(models)
	if n == 0 {
		return nil
	}

	var start int
	switch group.Strategy {
	case "round-robin":
		// rrStart 已由调用方按 len(models) 取模，这里再兜底 clamp，
		// 防止外部传入越界值（高危1：索引与模型列表快照不一致）。
		start = ((rrStart % n) + n) % n
	case "random":
		start = rand.Intn(n)
	default: // sequential / 未知策略
		start = 0
	}

	ordered := make([]config.ModelRef, 0, n)
	for i := 0; i < n; i++ {
		ordered = append(ordered, models[(start+i)%n])
	}
	return ordered
}

// nextRoundRobinIndex 在持有 roundRobinMutex 的前提下，读取并推进
// 模型组的轮询游标，返回本次应使用的起点下标（已对 modelCount 取模）。
// modelCount<=0 时返回 0，调用方需保证不会以空组进入。
func (s *Server) nextRoundRobinIndex(groupID string, modelCount int) int {
	if modelCount <= 0 {
		return 0
	}
	s.roundRobinMutex.Lock()
	defer s.roundRobinMutex.Unlock()
	idx := s.roundRobinIndex[groupID] % modelCount
	if idx < 0 {
		idx = 0
	}
	s.roundRobinIndex[groupID] = (idx + 1) % modelCount
	return idx
}

// buildCandidates 组合上面两步，返回本次请求的有序候选模型列表。
// 这是请求热路径调用的唯一入口，替代旧的 selectModel。
func (s *Server) buildCandidates(group *config.ModelGroupConfig) []config.ModelRef {
	if group == nil || len(group.Models) == 0 {
		return nil
	}
	rrStart := 0
	if group.Strategy == "round-robin" {
		rrStart = s.nextRoundRobinIndex(group.ID, len(group.Models))
	}
	return orderedCandidates(group, rrStart)
}

// maxAttempts 计算一次请求最多尝试多少个候选模型。
// 语义：MaxRetries 表示首次失败之后**额外**的重试次数，因此总尝试数
// = MaxRetries + 1，但不超过候选模型数量（每个候选最多用一次）。
// MaxRetries<0 视为 0。
func maxAttempts(maxRetries, candidateCount int) int {
	if candidateCount <= 0 {
		return 0
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	attempts := maxRetries + 1
	if attempts > candidateCount {
		attempts = candidateCount
	}
	return attempts
}

// shouldRetryStatus 判断给定的上游 HTTP 状态码是否值得换下一个模型重试。
// 借鉴 new-api 的状态码区间策略，针对个人网关场景做了精简：
//   - 2xx/3xx: 成功，不重试
//   - 408 (请求超时), 409, 425, 429 (限流): 重试
//   - 5xx: 重试，但 501(未实现)/505 视为协议级错误不重试；
//     504(网关超时)/524 由调用方根据是否流式自行决定，这里默认重试，
//     因为换一个上游模型通常能绕开单点超时
//   - 4xx (除上面列出的): 客户端错误，重试同样会失败，不重试
//
// statusCode<=0 表示连接层失败（DNS/拨号/读取错误），一律重试。
func shouldRetryStatus(statusCode int) bool {
	if statusCode <= 0 {
		return true // 连接错误：换一个上游
	}
	if statusCode >= 200 && statusCode < 400 {
		return false
	}
	switch statusCode {
	case 408, 409, 425, 429:
		return true
	case 501, 505:
		return false
	}
	if statusCode >= 500 {
		return true
	}
	return false
}
