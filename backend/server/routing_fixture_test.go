package server

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func presetGroup(t *testing.T, platform, upstreamURL string) []config.ModelGroupConfig {
	t.Helper()
	return []config.ModelGroupConfig{{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "preset-model", Name: "preset-model", BaseURL: upstreamURL, APIKey: "k", Platform: platform, ToolsCapable: true, VisionCapable: true}},
	}}
}

// Fixtures use the same source/binding persistence as production, not config imports.
func seedRoutingGroups(t *testing.T, s *Server, groups []config.ModelGroupConfig) {
	t.Helper()
	sources := map[string]storage.ModelSource{}
	for _, group := range groups {
		refs := []string{}
		for index, model := range group.Models {
			sourceID := model.SourceID
			if sourceID == "" {
				sourceID = fmt.Sprintf("test-%s-%d", group.ID, index)
			}
			platform := fixturePlatform(model.Platform)
			source := storage.ModelSource{ID: sourceID, Name: sourceID, BaseURL: model.BaseURL, APIKey: model.APIKey, Platform: platform, Enabled: true}
			if err := s.store.UpsertSource(t.Context(), source); err != nil {
				t.Fatal("save fixture source", err)
			}
			sources[sourceID] = source
			row := storage.Model{ID: model.ID, Name: model.Name, BaseURL: model.BaseURL, Platform: platform, Enabled: true, Available: true, Origin: "manual", ToolsCapable: model.ToolsCapable, VisionCapable: model.VisionCapable}
			if _, err := s.store.MergeSourceModels(t.Context(), source, []storage.Model{row}); err != nil {
				t.Fatal(err)
			}
			refs = append(refs, sourceID+":"+model.ID)
		}
		row := storage.ModelGroup{ID: group.ID, Name: group.Name, Enabled: group.Enabled, Models: refs,
			Strategy: group.Strategy, MaxRetries: group.MaxRetries, RetryInterval: group.RetryInterval,
			MaxConcurrency: group.MaxConcurrency, DailyLimitMaxRequests: group.DailyLimitMaxRequests,
			DailyLimitMaxTokens: group.DailyLimitMaxTokens, Type: group.Type, MaxTokens: group.MaxTokens,
			VisionCapable: group.VisionCapable != nil && *group.VisionCapable,
			ToolsCapable:  group.ToolsCapable != nil && *group.ToolsCapable}
		if err := s.store.UpsertGroup(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	seedFixtureBindings(t, s, sources)
	s.invalidateRouteCache()
}

// Gateway fixtures reuse deterministic offline proofs for identical contracts.
// Tests of source saving, runtime recovery and verification still run production
// verification directly; no passing evidence is fabricated here.
var fixtureBindingReports sync.Map

// Resolve policies, model/provider context and capabilities before selecting a
// cached proof. A changed contract must never inherit another fixture's result.
func verifyFixtureBinding(t *testing.T, s *Server, binding storage.ProtocolBinding) []protocol.CombinationReport {
	t.Helper()
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	view := service.View()
	upstream, ok := view.Pin(binding.Binding.ProtocolID)
	if !ok {
		t.Fatal("fixture protocol is not active", binding.Binding.ProtocolID)
	}
	policies, bindings, _, err := s.store.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bindings = slices.DeleteFunc(bindings, func(old storage.ProtocolBinding) bool { return old.Key() == binding.Key() })
	bindings = append(bindings, binding)
	models, err := s.store.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sources, err := s.store.ListSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	checks, err := s.resolveBindingConversions(t.Context(), view, policies, bindings, binding, s.bindingModelReferences(binding, models, collectSourceKeys(sources)))
	if err != nil {
		t.Fatal(err)
	}
	var reports []protocol.CombinationReport
	for _, check := range checks {
		key := check.ingress.Hash() + ":" + upstream.Hash() + ":" + check.conversion.Hash + ":" + check.conversion.ContextHash() + ":" + protocol.CapabilityContractHash(binding.Binding.Capabilities)
		entry, _ := fixtureBindingReports.LoadOrStore(key, sync.OnceValue(func() []protocol.CombinationReport {
			return protocol.VerifyBindingProfiles(context.Background(), check.ingress, upstream, binding.Binding.Capabilities, check.conversion)
		}))
		reports = append(reports, entry.(func() []protocol.CombinationReport)()...)
	}
	return reports
}

func seedFixtureBindings(t *testing.T, s *Server, sources map[string]storage.ModelSource) {
	t.Helper()
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	view := service.View()
	bindings := []storage.ProtocolBinding{}
	for _, source := range sources {
		id, err := protocolIDForPlatform(source.Platform)
		if err != nil {
			t.Fatal(err)
		}
		compiled, ok := view.Pin(id)
		if !ok {
			t.Fatal("fixture protocol is not active", id)
		}
		bindings = append(bindings, makeProtocolBinding(storage.BindingKey{Kind: "source", SourceID: source.ID}, compiled))
	}
	for _, binding := range bindings {
		binding.Combinations = verifyFixtureBinding(t, s, binding)
		if !hasPassingGatewayCombination(binding.Combinations) {
			t.Fatal("fixture has no verified gateway route", binding.SourceID)
		}
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
}

// Wire matrix cases name formats; source records always use protocol IDs.
func fixturePlatform(wire string) string {
	switch wire {
	case "openai", "openai_chat":
		return "custom:" + protocol.PresetChatCompletionsID
	case "responses":
		return "custom:" + protocol.PresetResponsesID
	case "claude", "anthropic":
		return "custom:" + protocol.PresetAnthropicID
	case "gemini":
		return "custom:" + protocol.PresetGeminiID
	default:
		return wire
	}
}
