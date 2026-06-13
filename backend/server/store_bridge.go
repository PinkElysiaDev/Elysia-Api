package server

import (
	"context"
	"strings"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) importLegacyConfig() error {
	if s.store == nil {
		return nil
	}
	ctx := context.Background()
	var imported bool
	if ok, err := s.store.GetSetting(ctx, "legacyConfigImported", &imported); err == nil && ok && imported {
		return nil
	}
	tokens := make([]storage.APIToken, 0, len(s.config.GetTokens()))
	for _, token := range s.config.GetTokens() {
		if strings.TrimSpace(token.Token) == "" {
			continue
		}
		name := token.Name
		if name == "" {
			name = shortTokenHash(token.Token)
		}
		tokens = append(tokens, storage.APIToken{Name: name, Token: token.Token, Enabled: token.Enabled})
	}

	modelsByID := map[string]storage.Model{}
	groups := make([]storage.ModelGroup, 0, len(s.config.GetGroups()))
	for _, group := range s.config.GetGroups() {
		modelIDs := make([]string, 0, len(group.Models))
		for _, model := range group.Models {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			modelIDs = append(modelIDs, model.ID)
			modelsByID[model.ID] = storage.Model{
				ID:        model.ID,
				Name:      model.Name,
				BaseURL:   model.BaseURL,
				APIKey:    model.APIKey,
				Platform:  model.Platform,
				Type:      group.Type,
				MaxTokens: group.MaxTokens,
				Available: true,
			}
		}
		groups = append(groups, storage.ModelGroup{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled, Models: modelIDs,
			Strategy: group.Strategy, MaxRetries: group.MaxRetries, RetryInterval: group.RetryInterval,
			MaxConcurrency: group.MaxConcurrency, DailyLimitMaxRequests: group.DailyLimitMaxRequests,
			DailyLimitMaxTokens: group.DailyLimitMaxTokens, Type: group.Type, MaxTokens: group.MaxTokens,
			VisionCapable: group.VisionCapable != nil && *group.VisionCapable,
			ToolsCapable:  group.ToolsCapable != nil && *group.ToolsCapable,
		})
	}
	models := make([]storage.Model, 0, len(modelsByID))
	for _, model := range modelsByID {
		models = append(models, model)
	}
	if err := s.store.ImportLegacyConfig(ctx, tokens, groups, models); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, "legacyConfigImported", true)
}

func (s *Server) getGroups() []config.ModelGroupConfig {
	if s.store == nil {
		return s.config.GetGroups()
	}
	if !s.ensureRouteCache() {
		return s.config.GetGroups() // 缓存装配失败时回退
	}
	s.routeCacheMu.RLock()
	defer s.routeCacheMu.RUnlock()
	return s.cachedGroups
}

func (s *Server) findGroupByName(name string) *config.ModelGroupConfig {
	for _, group := range s.getGroups() {
		if group.Name == name {
			groupCopy := group
			return &groupCopy
		}
	}
	return nil
}

func (s *Server) findAccessToken(token string) (config.AccessToken, bool) {
	if strings.TrimSpace(token) == "" {
		return config.AccessToken{}, false
	}
	if s.store != nil && s.ensureRouteCache() {
		s.routeCacheMu.RLock()
		item, ok := s.cachedTokens[token]
		s.routeCacheMu.RUnlock()
		if ok {
			return item, true
		}
		// 缓存未命中时不再回查 DB：缓存是 token 表的完整快照，
		// 未命中即不存在（避免给暴力探测留 DB 查询放大面）。
		return s.config.FindAccessToken(token)
	}
	return s.config.FindAccessToken(token)
}
