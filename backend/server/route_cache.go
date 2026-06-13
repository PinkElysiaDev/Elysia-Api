package server

import (
	"context"

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
	s.routeCacheMu.RUnlock()
	if loaded {
		return true
	}

	// 装配在锁外完成，减少持锁时间；多个并发请求可能重复装配一次，
	// 但结果一致且幂等，可接受。
	groups, okGroups := s.assembleGroupsFromStore()
	tokens, okTokens := s.loadTokensFromStore()
	if !okGroups || !okTokens {
		return false // 装配失败，调用方回退
	}

	s.routeCacheMu.Lock()
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
	s.cachedGroups = nil
	s.cachedTokens = nil
	s.routeCacheMu.Unlock()
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
	modelMap := make(map[string]storage.Model, len(models))
	for _, model := range models {
		modelMap[model.ID] = model
	}
	converted := make([]config.ModelGroupConfig, 0, len(groups))
	for _, group := range groups {
		refs := make([]config.ModelRef, 0, len(group.Models))
		for _, modelID := range group.Models {
			model, ok := modelMap[modelID]
			if !ok || !model.Available {
				continue
			}
			refs = append(refs, config.ModelRef{ID: model.ID, Name: model.Name, BaseURL: model.BaseURL, APIKey: model.APIKey, Platform: model.Platform})
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
		tokens[item.Token] = config.AccessToken{Name: item.Name, Token: item.Token, Enabled: item.Enabled}
	}
	return tokens, true
}
