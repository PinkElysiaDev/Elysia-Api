package server

import (
	"context"
	"fmt"
	"sort"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) prepareProtocolUpgradeBindings(ctx context.Context, input protocolUpgradeInput, preview *protocolUpgradePreview, definitions map[string]*protocol.Compiled) error {
	existing, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return err
	}
	bindings := map[storage.BindingKey]storage.ProtocolBinding{}
	for _, binding := range existing {
		bindings[binding.Key()] = binding
	}
	hasOverride := map[storage.BindingKey]bool{}
	for _, binding := range input.Bindings {
		key := binding.Key()
		if hasOverride[key] {
			return fmt.Errorf("duplicate migration binding")
		}
		hasOverride[key], bindings[key] = true, binding
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return err
	}
	models, err := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		return err
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return err
	}
	validTargets := map[storage.BindingKey]bool{}
	targetCapabilities := map[storage.BindingKey]struct{ tools, media bool }{}
	for _, source := range sources {
		key := storage.BindingKey{Kind: "source", SourceID: source.ID}
		validTargets[key] = true
		if _, exists := bindings[key]; !exists {
			binding, issue := makeUpgradeBinding(key, source.Platform, definitions)
			if issue != nil {
				preview.Issues = append(preview.Issues, *issue)
				continue
			}
			bindings[key] = binding
		}
	}
	for _, model := range models {
		key := storage.BindingKey{Kind: "model", SourceID: model.SourceID, ModelID: model.ID}
		validTargets[key] = true
		targetCapabilities[key] = struct{ tools, media bool }{model.ToolsCapable, model.VisionCapable}
		if _, exists := bindings[key]; !exists {
			if parent, ok := bindings[storage.BindingKey{Kind: "source", SourceID: model.SourceID}]; ok && parent.Unbound {
				bindings[key] = storage.ProtocolBinding{Kind: "model", SourceID: model.SourceID, ModelID: model.ID, Unbound: true}
				continue
			}
			binding, issue := makeUpgradeBinding(key, model.Platform, definitions)
			if issue != nil {
				preview.Issues = append(preview.Issues, *issue)
				continue
			}
			stripModelDisallowedCapabilities(binding.Binding.Capabilities, model.ToolsCapable, model.VisionCapable)
			if model.ToolsCapable && !hasToolCapability(binding.Binding.Capabilities) {
				preview.Issues = append(preview.Issues, migrationIssue(binding.Binding.ProtocolID, "/models/"+model.ID, "existing model tool capability cannot be fulfilled by this protocol"))
			}
			if model.VisionCapable && !hasMediaCapability(binding.Binding.Capabilities) {
				preview.Issues = append(preview.Issues, migrationIssue(binding.Binding.ProtocolID, "/models/"+model.ID, "existing model media capability cannot be fulfilled by this protocol"))
			}
			bindings[key] = binding
		}
	}
	for _, group := range groups {
		key := storage.BindingKey{Kind: "group", GroupID: group.ID}
		validTargets[key] = true
		targetCapabilities[key] = struct{ tools, media bool }{group.ToolsCapable, group.VisionCapable}
		// Existing group booleans remain the route constraint for unbound groups.
		// Choosing an ingress here would remove their other public entrypoints.
	}
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for key, binding := range bindings {
		if !validTargets[key] {
			preview.Issues = append(preview.Issues, migrationIssue(binding.Binding.ProtocolID, "/bindings", "binding target does not exist or has conflicting identity fields"))
			continue
		}
		if binding.Unbound {
			preview.Bindings = append(preview.Bindings, binding)
			continue
		}
		if limits, exists := targetCapabilities[key]; exists {
			if err := checkBindingCapabilities(binding.Binding.Capabilities, limits.tools, limits.media); err != nil {
				preview.Issues = append(preview.Issues, migrationIssue(binding.Binding.ProtocolID, "/bindings", err.Error()))
			}
		}
		target := definitions[binding.Binding.ProtocolID]
		if target != nil {
			binding.Binding.RevisionHash = target.Hash()
		}
		issues := protocol.CheckBinding(binding.Binding, target)
		if binding.Kind == "group" {
			issues = protocol.CheckIngressBinding(binding.Binding, target)
		}
		preview.Issues = append(preview.Issues, issues...)
		binding.Combinations = nil

		preview.Bindings = append(preview.Bindings, binding)
	}
	sort.Slice(preview.Bindings, func(i, j int) bool {
		left, right := preview.Bindings[i].Key(), preview.Bindings[j].Key()
		return fmt.Sprint(left) < fmt.Sprint(right)
	})
	if protocol.IssuesError(preview.Issues) == nil {
		policies, _, _, err := s.store.ConversionSnapshot(ctx)
		if err != nil {
			return err
		}
		verified, err := s.verifyConversionBindings(ctx, conversionDefinitions(definitions), policies, preview.Bindings)
		if err != nil {
			return err
		}
		preview.Bindings = verified
	}
	preview.plan.Bindings = preview.Bindings
	return nil
}

func makeUpgradeBinding(key storage.BindingKey, platform string, definitions map[string]*protocol.Compiled) (storage.ProtocolBinding, *protocol.ConversionIssue) {
	id, err := protocolIDForPlatform(platform)
	if err != nil {
		issue := migrationIssue("", "/bindings", err.Error())
		return storage.ProtocolBinding{}, &issue
	}
	compiled := definitions[id]
	if compiled == nil {
		issue := migrationIssue(id, "/bindings", "source or model references a missing or invalid protocol")
		return storage.ProtocolBinding{}, &issue
	}
	return makeProtocolBinding(key, compiled), nil
}

func makeProtocolBinding(key storage.BindingKey, compiled *protocol.Compiled) storage.ProtocolBinding {
	capabilities := compiled.Definition().Capabilities
	transports := []protocol.Transport{}
	hasTransport := map[protocol.Transport]bool{}
	for _, operation := range compiled.Operations() {
		if operation.Kind != "generate" || hasTransport[operation.Transport] {
			continue
		}
		hasTransport[operation.Transport] = true
		transports = append(transports, operation.Transport)
	}
	sort.Slice(transports, func(i, j int) bool { return transports[i] < transports[j] })
	return storage.ProtocolBinding{Kind: key.Kind, SourceID: key.SourceID, ModelID: key.ModelID, GroupID: key.GroupID, Binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: capabilities, Transports: transports}}
}
