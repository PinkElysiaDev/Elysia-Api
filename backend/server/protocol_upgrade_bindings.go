package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

type upgradeBindingKey struct{ kind, source, model, group string }

func upgradeBindingIdentity(binding storage.ProtocolBinding) upgradeBindingKey {
	return upgradeBindingKey{binding.Kind, binding.SourceID, binding.ModelID, binding.GroupID}
}

func (s *Server) prepareProtocolUpgradeBindings(ctx context.Context, input protocolUpgradeInput, preview *protocolUpgradePreview, definitions map[string]*protocol.Compiled) error {
	existing, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return err
	}
	bindings := map[upgradeBindingKey]storage.ProtocolBinding{}
	for _, binding := range existing {
		bindings[upgradeBindingIdentity(binding)] = binding
	}
	hasOverride := map[upgradeBindingKey]bool{}
	for _, binding := range input.Bindings {
		key := upgradeBindingIdentity(binding)
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
	validTargets := map[upgradeBindingKey]bool{}
	targetCapabilities := map[upgradeBindingKey]struct{ tools, media bool }{}
	for _, source := range sources {
		key := upgradeBindingKey{kind: "source", source: source.ID}
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
		key := upgradeBindingKey{kind: "model", source: model.SourceID, model: model.ID}
		validTargets[key] = true
		targetCapabilities[key] = struct{ tools, media bool }{model.ToolsCapable, model.VisionCapable}
		if _, exists := bindings[key]; !exists {
			if parent, ok := bindings[upgradeBindingKey{kind: "source", source: model.SourceID}]; ok && parent.Unbound {
				bindings[key] = storage.ProtocolBinding{Kind: "model", SourceID: model.SourceID, ModelID: model.ID, Unbound: true}
				continue
			}
			binding, issue := makeUpgradeBinding(key, model.Platform, definitions)
			if issue != nil {
				preview.Issues = append(preview.Issues, *issue)
				continue
			}
			if !model.ToolsCapable {
				for _, capability := range []protocol.Capability{protocol.FunctionToolsCapability, protocol.FreeTextToolsCapability, protocol.ServerToolsCapability} {
					delete(binding.Binding.Capabilities, capability)
				}
			}
			if !model.VisionCapable {
				for _, capability := range []protocol.Capability{protocol.ImagesCapability, protocol.AudioCapability, protocol.VideoCapability} {
					delete(binding.Binding.Capabilities, capability)
				}
			}
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
		key := upgradeBindingKey{kind: "group", group: group.ID}
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
	verifiedCombinations := map[string][]protocol.CombinationReport{}
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
		if target != nil && len(issues) == 0 && binding.Kind != "group" {
			encoded, err := json.Marshal(binding.Binding.Capabilities)
			if err != nil {
				return err
			}
			key := target.Hash() + string(encoded)
			binding.Combinations = verifiedCombinations[key]
			if binding.Combinations == nil {
				binding.Combinations = verifyUpgradeCombinations(ctx, definitions, ids, target, binding.Binding.Capabilities)
				verifiedCombinations[key] = binding.Combinations
			}
			if !hasPassingGatewayCombination(binding.Combinations) {
				preview.Issues = append(preview.Issues, migrationIssue(binding.Binding.ProtocolID, "/bindings", "no ingress verifies the complete binding capability contract"))
			}
		}
		preview.Bindings = append(preview.Bindings, binding)
	}
	sort.Slice(preview.Bindings, func(i, j int) bool {
		left, right := upgradeBindingIdentity(preview.Bindings[i]), upgradeBindingIdentity(preview.Bindings[j])
		return fmt.Sprint(left) < fmt.Sprint(right)
	})
	preview.plan.Bindings = preview.Bindings
	return nil
}

func verifyUpgradeCombinations(ctx context.Context, definitions map[string]*protocol.Compiled, ids []string, target *protocol.Compiled, capabilities protocol.CapabilitySet) []protocol.CombinationReport {
	var reports []protocol.CombinationReport
	for _, id := range ids {
		ingress := definitions[id]
		if ingress.Supports(protocol.DecodeRequest) && (ingress.Supports(protocol.EncodeResponse) || ingress.Supports(protocol.EncodeEvent)) {
			reports = append(reports, protocol.VerifyBindingProfiles(ctx, ingress, target, capabilities)...)
		}
	}
	return reports
}

func makeUpgradeBinding(key upgradeBindingKey, platform string, definitions map[string]*protocol.Compiled) (storage.ProtocolBinding, *protocol.ConversionIssue) {
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

func makeProtocolBinding(key upgradeBindingKey, compiled *protocol.Compiled) storage.ProtocolBinding {
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
	return storage.ProtocolBinding{Kind: key.kind, SourceID: key.source, ModelID: key.model, GroupID: key.group, Binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: capabilities, Transports: transports}}
}
