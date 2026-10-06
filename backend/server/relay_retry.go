package server

import (
	"math/rand"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

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
	// 上限与流事件一致：保尾不保头（最后的错误最接近根因）。多 key priority
	// 展开可让候选数远超预期，不加限会让记录随候选数线性膨胀。
	if len(record.RetryEvents) >= RetryEventsCacheMax {
		record.RetryEvents = append(record.RetryEvents[:0], record.RetryEvents[1:]...)
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
		// rrStart 由 nextRoundRobinIndex 产出，恒在 [0, n) 内（见其实现）。
		start = rrStart % n
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
	// map 零值 0、写入值 (idx+1)%modelCount 恒非负，无需负数钳制。
	idx := s.roundRobinIndex[groupID] % modelCount
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

func (s *Server) expandCandidatesByKeyStrategy(candidates []config.ModelRef) []config.ModelRef {
	multi := false
	for i := range candidates {
		if len(candidates[i].APIKeys) > 1 {
			multi = true
			break
		}
	}
	if !multi {
		return candidates
	}
	expanded := make([]config.ModelRef, 0, len(candidates))
	for _, candidate := range candidates {
		clone := candidate
		clone.APIKeys = nil
		if len(candidate.APIKeys) == 0 {
			// config 直配路径可能声明了策略但没配 apiKeys:索引/随机会 panic
			// (rand.Intn(0)),回落单 key 原样。
			expanded = append(expanded, clone)
			continue
		}
		switch storage.SourceKeyStrategy(candidate.KeyStrategy) {
		case storage.KeyStrategyPriority:
			for _, key := range candidate.APIKeys {
				withKey := clone
				withKey.APIKey = key
				expanded = append(expanded, withKey)
			}
		case storage.KeyStrategyRoundRobin:
			clone.APIKey = candidate.APIKeys[s.nextSourceKeyIndex(candidate.SourceID, len(candidate.APIKeys))]
			expanded = append(expanded, clone)
		case storage.KeyStrategyRandom:
			clone.APIKey = candidate.APIKeys[rand.Intn(len(candidate.APIKeys))]
			expanded = append(expanded, clone)
		default: // single / 未知策略：单 key 原样
			expanded = append(expanded, clone)
		}
	}
	return expanded
}

// nextSourceKeyIndex 读取并推进源级 round-robin key 游标（方向6）。
// 游标是进程内存态：重启归零可接受（key 轮转无持久化必要，对照 new-api 的
// polling 需持久化的取舍，这里选简单实现）。
func (s *Server) nextSourceKeyIndex(sourceID string, count int) int {
	if count <= 0 {
		return 0
	}
	s.keyRRMutex.Lock()
	defer s.keyRRMutex.Unlock()
	if s.keyRRIndex == nil {
		s.keyRRIndex = make(map[string]int)
	}
	// map 零值 0、写入值 idx+1 恒非负，无需负数钳制。
	idx := s.keyRRIndex[sourceID] % count
	s.keyRRIndex[sourceID] = idx + 1
	return idx
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
