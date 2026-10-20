package routing

import (
	"math/rand"
	"sync"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// Scheduler owns independent model-group and source-key cursors. Its zero value
// is ready for concurrent use; callers never manipulate cursor maps or locks.
type Scheduler struct {
	groups cursor
	keys   cursor
}

type cursor struct {
	mu        sync.Mutex
	positions map[string]int
}

func (c *cursor) next(id string, count int, wrap bool) int {
	if count <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.positions == nil {
		c.positions = make(map[string]int)
	}
	index := c.positions[id] % count
	c.positions[id] = index + 1
	if wrap {
		c.positions[id] %= count
	}
	return index
}

func (c *cursor) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.positions, id)
}

func (s *Scheduler) ForgetGroup(id string)  { s.groups.forget(id) }
func (s *Scheduler) ForgetSource(id string) { s.keys.forget(id) }

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
// rrStart is obtained from the scheduler's synchronized cursor.
func orderedCandidates(group *config.ModelGroupConfig, rrStart int) []config.ModelRef {
	models := group.Models
	n := len(models)
	if n == 0 {
		return nil
	}

	var start int
	switch group.Strategy {
	case "round-robin":
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

// Candidates returns all models in failover order without modifying the group.
func (s *Scheduler) Candidates(group *config.ModelGroupConfig) []config.ModelRef {
	if group == nil || len(group.Models) == 0 {
		return nil
	}
	rrStart := 0
	if group.Strategy == "round-robin" {
		rrStart = s.groups.next(group.ID, len(group.Models), true)
	}
	return orderedCandidates(group, rrStart)
}

func (s *Scheduler) ExpandKeys(candidates []config.ModelRef) []config.ModelRef {
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
			// Source cursors retain the next ordinal: different models may have
			// different permitted-key counts for the same source.
			clone.APIKey = candidate.APIKeys[s.keys.next(candidate.SourceID, len(candidate.APIKeys), false)]
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

// MaxAttempts 计算一次请求最多尝试多少个候选模型。
// 语义：MaxRetries 表示首次失败之后**额外**的重试次数，因此总尝试数
// = MaxRetries + 1，但不超过候选模型数量（每个候选最多用一次）。
// MaxRetries<0 视为 0。
func MaxAttempts(maxRetries, candidateCount int) int {
	if candidateCount <= 0 {
		return 0
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries >= candidateCount {
		return candidateCount
	}
	return maxRetries + 1
}
