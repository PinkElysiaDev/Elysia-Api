package server

import (
	"context"
	"strings"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/routing"
)

// All source/group/token mutations invalidate this snapshot after persistence.
func (s *Server) invalidateRouteCache() { s.routes.Invalidate() }

func (s *Server) forgetGroupRuntimeState(groupID string) {
	if groupID == "" {
		return
	}
	s.limiter.Forget(groupID)
	s.scheduler.ForgetGroup(groupID)
	s.affinity.removeGroup(groupID)
}

func (s *Server) routeSnapshot() *routing.Snapshot {
	if s.store == nil {
		return nil
	}
	snapshot, err := s.routes.Load(context.Background(), func(ctx context.Context) (*routing.Snapshot, error) {
		return routing.Load(ctx, s.store)
	})
	if err != nil {
		s.logEvent("error").Err(err).Msg("failed to load routes from sqlite")
	}
	return snapshot
}

func (s *Server) getGroups() []config.ModelGroupConfig {
	if snapshot := s.routeSnapshot(); snapshot != nil {
		return snapshot.Groups
	}
	return nil
}

func (s *Server) findGroupByName(name string) *config.ModelGroupConfig {
	for _, group := range s.getGroups() {
		if group.Name == name {
			// Copy only the matched group; the cached snapshot remains read-only.
			groupCopy := group
			return &groupCopy
		}
	}
	return nil
}

func (s *Server) findAccessToken(token string) (config.AccessToken, bool) {
	if strings.TrimSpace(token) != "" {
		if snapshot := s.routeSnapshot(); snapshot != nil {
			item, ok := snapshot.Tokens[token]
			return item, ok
		}
	}
	return config.AccessToken{}, false
}
