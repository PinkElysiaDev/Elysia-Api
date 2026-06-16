package server

import (
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
)

// 渠道亲和性（channel affinity / sticky routing）：
// 让"同一调用方（API key）+ 同一模型组"的连续请求在一个短时间窗内优先
// 复用上次成功的具体模型。价值：
//   - 提升上游 prompt 缓存命中率（Anthropic/OpenAI 等按前缀缓存，打回同一
//     上游更便宜更快）；
//   - 同一会话期间模型版本/行为一致。
//
// 实现为带 TTL 的内存映射。仅影响候选模型的"排序"（把粘连模型提到最前），
// 不改变可用候选集合，因此与故障转移完全兼容：粘连模型失败时照常转移到
// 其他候选，并在成功后更新粘连目标。


type affinityEntry struct {
	modelName string
	expiresAt time.Time
}

type affinityCache struct {
	mu      sync.Mutex
	entries map[string]affinityEntry
}

func newAffinityCache() *affinityCache {
	return &affinityCache{entries: make(map[string]affinityEntry)}
}

func affinityKey(keyHash, groupID string) string {
	return keyHash + "\x00" + groupID
}

// get 返回未过期的粘连模型名；无或已过期返回 ""。
func (a *affinityCache) get(keyHash, groupID string, now time.Time) string {
	if a == nil || keyHash == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.entries[affinityKey(keyHash, groupID)]
	if !ok || now.After(entry.expiresAt) {
		return ""
	}
	return entry.modelName
}

// set 记录一次成功的模型，刷新 TTL。
func (a *affinityCache) set(keyHash, groupID, modelName string, now time.Time) {
	if a == nil || keyHash == "" || modelName == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// 顺手清理已过期项，控制 map 增长（个人网关 key 数量有限，开销可忽略）。
	for k, v := range a.entries {
		if now.After(v.expiresAt) {
			delete(a.entries, k)
		}
	}
	a.entries[affinityKey(keyHash, groupID)] = affinityEntry{modelName: modelName, expiresAt: now.Add(AffinityTTL)}
}

// applyAffinity 把粘连模型（若存在且仍在候选集合内）提到候选列表最前面，
// 其余顺序保持不变。返回新切片，不修改入参。
func applyAffinity(candidates []config.ModelRef, stickyModel string) []config.ModelRef {
	if stickyModel == "" || len(candidates) <= 1 {
		return candidates
	}
	idx := -1
	for i, c := range candidates {
		if c.Name == stickyModel {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return candidates // 不在候选集 或 已在最前
	}
	reordered := make([]config.ModelRef, 0, len(candidates))
	reordered = append(reordered, candidates[idx])
	reordered = append(reordered, candidates[:idx]...)
	reordered = append(reordered, candidates[idx+1:]...)
	return reordered
}
