package server

import (
	"context"
	"strings"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// routeCache 借鉴 new-api 的内存缓存 + 配置热更新（SyncOptions）思路：
// 把每次请求都要用到的"模型组装配结果"和"API token 表"载入内存，
// 请求热路径只读内存，避免每请求 3+ 次串行 SQLite 查询与 ListGroups 的
// N+1 子查询。所有写操作（source/group/token 的增删、模型刷新、/__reload）
// 调用 invalidateRouteCache 失效缓存，下次读取时按需重建。

// ensureRouteCache 在缓存未加载时从 store 装配并填充缓存。
// 调用方不应持有 routeCacheMu。store 为空（仅内存配置模式）时不缓存，
// 由调用方回退到 s.config。
func (s *Server) ensureRouteCache() bool {
	if s.store == nil {
		return false
	}
	s.routeCacheMu.RLock()
	loaded := s.routeCacheLoaded
	// 代际计数：装配期间若有写操作触发失效，gen 会递增——旧快照不允许
	// 带着 loaded=true 落缓存（已撤销的 token/已删模型会继续放行）。
	generation := s.routeCacheGeneration
	s.routeCacheMu.RUnlock()
	if loaded {
		return true
	}

	// 装配在锁外完成，减少持锁时间；多个并发请求可能重复装配一次，
	// 但结果一致且幂等，可接受（最终落缓存的以 gen 校验为准）。
	groups, okGroups := s.assembleGroupsFromStore()
	tokens, okTokens := s.loadTokensFromStore()
	if !okGroups || !okTokens {
		return false // 装配失败，调用方回退
	}

	s.routeCacheMu.Lock()
	if s.routeCacheGeneration != generation {
		// 装配期间发生失效：丢弃本快照，让下次读取重新装配。
		s.routeCacheMu.Unlock()
		return false
	}
	s.cachedGroups = groups
	s.cachedTokens = tokens
	s.routeCacheLoaded = true
	s.routeCacheMu.Unlock()
	return true
}

// invalidateRouteCache 标记缓存失效，下次读取时重建。
// 在所有写操作后调用。
func (s *Server) invalidateRouteCache() {
	s.routeCacheMu.Lock()
	s.routeCacheLoaded = false
	s.routeCacheGeneration++
	s.cachedGroups = nil
	s.cachedTokens = nil
	s.routeCacheMu.Unlock()
}

// sourceKeyMeta 是源级 key 调度元数据（方向6），装配时随 ModelRef 下发。
// keys 保留完整 SourceAPIKey：除 value 外还需按 key 的
// FetchedModels/AllowedModels 对每个模型过滤可服务该模型的 key 池
// （多 key 权限发现，见 KeyAllowsModel）。
type sourceKeyMeta struct {
	keys     []storage.SourceAPIKey
	strategy string
}

// assembleGroupsFromStore 一次性读取 groups + models 并装配成
// 运行时模型组（含解析后的 ModelRef），消除旧 getGroups 的 N+1。
func (s *Server) assembleGroupsFromStore() ([]config.ModelGroupConfig, bool) {
	ctx := context.Background()
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		s.logWarnf("failed to load model groups from sqlite: %v", err)
		return nil, false
	}
	models, err := s.store.ListModels(ctx)
	if err != nil {
		s.logWarnf("failed to load models from sqlite: %v", err)
		return nil, false
	}
	// 源级多 key 元数据：ModelRef 的 key 集合以源为准（models.api_key 冗余列仅作
	// 单 key 回退，不再随多 key 演进——避免大迁移）。
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		s.logWarnf("failed to load model sources from sqlite: %v", err)
		return nil, false
	}
	keyMeta := make(map[string]sourceKeyMeta, len(sources))
	for _, source := range sources {
		effective := source.EffectiveKeys()
		keys := make([]storage.SourceAPIKey, 0, len(effective))
		for _, key := range effective {
			keys = append(keys, key)
		}
		keyMeta[source.ID] = sourceKeyMeta{keys: keys, strategy: string(source.KeyStrategy)}
	}
	// 同时按复合键(sourceId:id)与裸 id 建索引：复合键精确命中（解决同名模型路由错乱），
	// 裸 id 用于向后兼容旧数据（models 元素无 ":" 前缀时回退）。
	modelByComposite := make(map[string]storage.Model, len(models))
	modelByID := make(map[string]storage.Model, len(models))
	for _, model := range models {
		modelByComposite[model.SourceID+":"+model.ID] = model
		if _, exists := modelByID[model.ID]; !exists {
			modelByID[model.ID] = model
		}
	}
	resolveModel := func(ref string) (storage.Model, bool) {
		if strings.Contains(ref, ":") {
			m, ok := modelByComposite[ref]
			return m, ok
		}
		m, ok := modelByID[ref] // 旧数据：裸 id 回退
		return m, ok
	}
	converted := make([]config.ModelGroupConfig, 0, len(groups))
	for _, group := range groups {
		refs := make([]config.ModelRef, 0, len(group.Models))
		for _, modelRef := range group.Models {
			// 可调度 = 健康可用（available，健康检测自动翻转）&& 用户启用（enabled，手动开关）。
			model, ok := resolveModel(modelRef)
			if !ok || !model.Available || !model.Enabled {
				continue
			}
			ref := config.ModelRef{ID: model.ID, Name: model.Name, BaseURL: model.BaseURL, APIKey: model.APIKey, Platform: model.Platform,
				VisionCapable: model.VisionCapable, ToolsCapable: model.ToolsCapable, SourceID: model.SourceID}
			if meta, ok := keyMeta[model.SourceID]; ok && len(meta.keys) > 0 {
				// 按模型过滤可服务该模型的 key（多 key 权限发现）：不在任何 key 的
				// 启用/拉取集合内的模型没有可用 key，该候选从组内剔除。
				permitted := make([]string, 0, len(meta.keys))
				for _, key := range meta.keys {
					if key.KeyAllowsModel(model.ID) {
						permitted = append(permitted, key.Value)
					}
				}
				if len(permitted) == 0 {
					s.logVerbose("[RouteCache] model %s (source %s) excluded: no api key in this source may serve it", model.ID, model.SourceID)
					continue
				}
				ref.APIKeys = permitted
				ref.KeyStrategy = meta.strategy
			}
			refs = append(refs, ref)
		}
		vision := group.VisionCapable
		tools := group.ToolsCapable
		converted = append(converted, config.ModelGroupConfig{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled, Models: refs,
			Strategy: group.Strategy, MaxRetries: group.MaxRetries, RetryInterval: group.RetryInterval,
			MaxConcurrency: group.MaxConcurrency, DailyLimitMaxRequests: group.DailyLimitMaxRequests,
			DailyLimitMaxTokens: group.DailyLimitMaxTokens, Type: group.Type, MaxTokens: group.MaxTokens,
			VisionCapable: &vision, ToolsCapable: &tools,
		})
	}
	return converted, true
}

// loadTokensFromStore 读取全部启用的 API token 到内存映射（token 明文 -> 元信息）。
func (s *Server) loadTokensFromStore() (map[string]config.AccessToken, bool) {
	items, err := s.store.ListAPITokens(context.Background())
	if err != nil {
		s.logWarnf("failed to load api tokens from sqlite: %v", err)
		return nil, false
	}
	tokens := make(map[string]config.AccessToken, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		tokens[item.Token] = config.AccessToken{Name: item.Name, Token: item.Token, Enabled: item.Enabled, AllowedGroups: item.AllowedGroups}
	}
	return tokens, true
}
