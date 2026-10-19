package server

import (
	"strings"

	"github.com/elysia-api/backend/config"
)

func (s *Server) getGroups() []config.ModelGroupConfig {
	if !s.ensureRouteCache() {
		return nil
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
	if strings.TrimSpace(token) == "" || !s.ensureRouteCache() {
		return config.AccessToken{}, false
	}
	s.routeCacheMu.RLock()
	item, ok := s.cachedTokens[token]
	s.routeCacheMu.RUnlock()
	return item, ok
}
