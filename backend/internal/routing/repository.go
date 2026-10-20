package routing

import (
	"context"
	"fmt"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// Repository is the read boundary required to build a route snapshot. The
// application supplies its store; routing does not open or own a database.
type Repository interface {
	ListGroups(context.Context) ([]storage.ModelGroup, error)
	ListModels(context.Context) ([]storage.Model, error)
	ListSources(context.Context) ([]storage.ModelSource, error)
	ListAPITokens(context.Context) ([]storage.APIToken, error)
}

func Load(ctx context.Context, repository Repository) (*Snapshot, error) {
	groups, err := AssembleGroups(ctx, repository)
	if err != nil {
		return nil, err
	}
	items, err := repository.ListAPITokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("load api tokens: %w", err)
	}
	tokens := make(map[string]config.AccessToken, len(items))
	for _, item := range items {
		if item.Enabled {
			tokens[item.Token] = config.AccessToken{Name: item.Name, Token: item.Token, Enabled: true, AllowedGroups: item.AllowedGroups, Scopes: item.Scopes}
		}
	}
	return &Snapshot{Groups: groups, Tokens: tokens}, nil
}

func AssembleGroups(ctx context.Context, repository Repository) ([]config.ModelGroupConfig, error) {
	groups, err := repository.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("load model groups: %w", err)
	}
	models, err := repository.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("load models: %w", err)
	}
	sources, err := repository.ListSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("load model sources: %w", err)
	}
	keys := IndexSources(sources)
	byIdentity := make(map[string]storage.Model, len(models))
	for _, model := range models {
		byIdentity[model.SourceID+":"+model.ID] = model
	}
	converted := make([]config.ModelGroupConfig, 0, len(groups))
	for _, group := range groups {
		refs := make([]config.ModelRef, 0, len(group.Models))
		for _, identity := range group.Models {
			model, found := byIdentity[identity]
			if !found || !model.Available || !model.Enabled {
				continue
			}
			if ref, ok := keys.Resolve(model); ok {
				refs = append(refs, ref)
			}
		}
		vision, tools := group.VisionCapable, group.ToolsCapable
		converted = append(converted, config.ModelGroupConfig{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled, Models: refs,
			Strategy: group.Strategy, MaxRetries: group.MaxRetries, RetryInterval: group.RetryInterval,
			MaxConcurrency: group.MaxConcurrency, DailyLimitMaxRequests: group.DailyLimitMaxRequests,
			DailyLimitMaxTokens: group.DailyLimitMaxTokens, Type: group.Type, MaxTokens: group.MaxTokens,
			VisionCapable: &vision, ToolsCapable: &tools,
		})
	}
	return converted, nil
}
