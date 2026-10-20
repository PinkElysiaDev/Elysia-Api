package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/elysia-api/backend/storage"
)

func (s *Server) launchSourceRefresh(sourceID string) bool {
	j := s.sourceRefreshJobs()
	if !j.begin(sourceID) {
		return false
	}
	go j.run(j.ctx, sourceID)
	return true
}

func (s *Server) noteSourceSaved(sourceID string, autoFetch bool) {
	s.sourceRefreshJobs().noteSaved(sourceID, autoFetch)
}

func (s *Server) sourceRefreshStateOf(sourceID string) sourceRefreshState {
	return s.sourceRefreshJobs().state(sourceID)
}

func (s *Server) startSourceRefreshByID(ctx context.Context, sourceID string) (bool, bool, error) {
	_, found, err := s.findSourceByID(ctx, sourceID)
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, fmt.Errorf("model source %q not found", sourceID)
	}
	started := s.launchSourceRefresh(sourceID)
	return started, !started, nil
}

func (s *Server) refreshSourceSync(ctx context.Context, sourceID string) (refreshSummary, error) {
	j := s.sourceRefreshJobs()
	if !j.begin(sourceID) {
		return refreshSummary{Added: []string{}, Removed: []string{}}, fmt.Errorf("model source %q is already refreshing or server is stopping", sourceID)
	}
	return j.run(ctx, sourceID)
}

// Lookup failures are not absence: callers preserving credentials must not
// overwrite an existing secret when persistence is temporarily unavailable.
func (s *Server) findSourceByID(ctx context.Context, id string) (storage.ModelSource, bool, error) {
	if s.store == nil || id == "" {
		return storage.ModelSource{}, false, nil
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return storage.ModelSource{}, false, err
	}
	for _, src := range sources {
		if src.ID == id {
			return src, true, nil
		}
	}
	return storage.ModelSource{}, false, nil
}

// HTTP and CLI deletion share persistence and runtime cleanup semantics.
func (s *Server) deleteSourceCascade(ctx context.Context, store *storage.Store, id string) error {
	if err := store.DeleteSource(ctx, id); err != nil {
		return err
	}
	s.sourceRefreshJobs().forget(id)
	s.logSystemEvent("warn", "model source deleted", map[string]any{"sourceId": id})
	s.scheduler.ForgetSource(id)
	s.invalidateRouteCache()
	return nil
}

func preserveSourceCredentials(item *storage.ModelSource, existing storage.ModelSource) {
	if strings.TrimSpace(item.APIKey) != "" {
		return
	}
	item.APIKey = existing.APIKey
	if len(item.APIKeys) == 0 {
		item.APIKeys = existing.APIKeys
	}
}

func (s *Server) validateSourceURLs(item storage.ModelSource) error {
	for _, raw := range []string{item.BaseURL, item.FetchBaseURL} {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		if err := s.validateOutbound(raw); err != nil {
			return fmt.Errorf("base url rejected: %w", err)
		}
	}
	return nil
}
