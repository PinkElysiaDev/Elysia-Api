package routing

import (
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

type sourceKeys struct {
	keys           []storage.SourceAPIKey
	strategy       string
	hasKeyPool     bool
	cacheSynthesis bool
	baseURL        string
}

// Sources indexes current source credentials once for routing and health probes.
// A model row's historical address and key are never used as fallback.
type Sources struct {
	byID map[string]sourceKeys
}

func IndexSources(sources []storage.ModelSource) Sources {
	index := Sources{byID: make(map[string]sourceKeys, len(sources))}
	for _, source := range sources {
		index.byID[source.ID] = sourceKeys{
			keys: source.EffectiveKeys(), hasKeyPool: len(source.APIKeys) > 0,
			strategy: string(source.KeyStrategy), baseURL: source.BaseURL,
			cacheSynthesis: source.CacheSynthesis,
		}
	}
	return index
}

// Resolve checks source/key permissions, not health: unhealthy models still need
// to be probed. Route assembly separately filters disabled/unavailable models.
func (s Sources) Resolve(model storage.Model) (config.ModelRef, bool) {
	meta, ok := s.byID[model.SourceID]
	if !ok || meta.baseURL == "" || (meta.hasKeyPool && len(meta.keys) == 0) {
		return config.ModelRef{}, false
	}
	ref := config.ModelRef{
		ID: model.ID, Name: model.Name, Platform: model.Platform, SourceID: model.SourceID,
		VisionCapable: model.VisionCapable, ToolsCapable: model.ToolsCapable,
		BaseURL: meta.baseURL, CacheSynthesis: meta.cacheSynthesis,
	}
	if len(meta.keys) > 0 {
		permitted := make([]string, 0, len(meta.keys))
		for _, key := range meta.keys {
			if key.KeyAllowsModel(model.ID) {
				permitted = append(permitted, key.Value)
			}
		}
		if len(permitted) == 0 {
			return config.ModelRef{}, false
		}
		ref.APIKey, ref.APIKeys, ref.KeyStrategy = permitted[0], permitted, meta.strategy
	}
	return ref, true
}
