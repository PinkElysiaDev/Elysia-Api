package server

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

// refreshProtocolRuntime prepares all evidence before any persistent mutation.
// An edited definition is verified as authored; only cataloged preset contents
// may be replaced. Saved drafts and deliberately inactive protocols stay intact.
func (s *Server) refreshProtocolRuntime(ctx context.Context, service *protocol.Service) error {
	baseline, err := s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return err
	}
	active, err := s.store.ListProtocolActivations(ctx)
	if err != nil || len(active) == 0 {
		return err
	}
	shipped, err := builtin.Definitions()
	if err != nil {
		return err
	}
	presets := map[string]protocol.Value{}
	for _, value := range shipped {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			return err
		}
		presets[definition.ID] = value
	}
	plan := storage.ProtocolUpgrade{Baseline: baseline}
	definitions := map[string]*protocol.Compiled{}
	ids := []string{}
	hasChanges := false
	for _, activation := range active {
		revision, err := s.store.ReadProtocolRevision(ctx, activation.ProtocolID, activation.RevisionHash)
		if err != nil {
			return err
		}
		definition := revision.Definition
		if replacement, exists := presets[activation.ProtocolID]; exists && builtin.IsPreviousDefinition(activation.ProtocolID, definition) {
			definition = replacement
		}
		compiled, issues := service.Validate(definition.Bytes())
		if err := protocol.IssuesError(issues); err != nil {
			return err
		}
		isReplaced := compiled.Hash() != revision.Hash
		report, err := s.store.ReadProtocolReport(ctx, activation.ProtocolID, compiled.Hash())
		if err != nil && !errors.Is(err, protocol.ErrNotFound) {
			return err
		}
		if err != nil || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			report = protocol.Verify(ctx, compiled)
			if err := protocol.IssuesError(protocol.CanActivate(compiled, report)); err != nil {
				return err
			}
			hasChanges = true
		}
		draft, err := s.store.ReadProtocolDraft(ctx, activation.ProtocolID)
		if err != nil && !errors.Is(err, protocol.ErrNotFound) {
			return err
		}
		if errors.Is(err, protocol.ErrNotFound) || (isReplaced && draft.Hash == revision.Hash) {
			draft = protocol.Draft{ProtocolID: activation.ProtocolID, Hash: compiled.Hash(), Definition: definition, UpdatedAt: time.Now().UTC()}
		}
		if isReplaced {
			revision = protocol.Revision{ProtocolID: activation.ProtocolID, Hash: compiled.Hash(), Definition: definition, CreatedAt: time.Now().UTC()}
		}
		hasChanges = hasChanges || isReplaced
		plan.Revisions = append(plan.Revisions, storage.ProtocolUpgradeRevision{Revision: revision, Report: report, Draft: draft})
		definitions[activation.ProtocolID] = compiled
		ids = append(ids, activation.ProtocolID)
	}
	sort.Strings(ids)
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		for _, report := range binding.Combinations {
			if report.CompilerVersion != protocol.CompilerVersion {
				hasChanges = true
			}
		}
	}
	if !hasChanges {
		return nil
	}
	combinations := map[string][]protocol.CombinationReport{}
	for _, binding := range bindings {
		compiled := definitions[binding.Binding.ProtocolID]
		if compiled == nil {
			return gatewayIssue(protocol.Identity{DefinitionID: binding.Binding.ProtocolID}, protocol.VerificationRequired, "/binding", "runtime refresh cannot enable an inactive bound protocol")
		}
		binding.Binding.RevisionHash = compiled.Hash()
		issues := protocol.CheckBinding(binding.Binding, compiled)
		if binding.Kind == "group" {
			issues = protocol.CheckIngressBinding(binding.Binding, compiled)
		}
		if err := protocol.IssuesError(issues); err != nil {
			return err
		}
		if binding.Kind != "group" {
			key := compiled.Hash() + protocol.CapabilityContractHash(binding.Binding.Capabilities)
			if reports, exists := combinations[key]; exists {
				binding.Combinations = reports
			} else {
				binding.Combinations = verifyUpgradeCombinations(ctx, definitions, ids, compiled, binding.Binding.Capabilities)
				combinations[key] = binding.Combinations
			}
			if !hasPassingGatewayCombination(binding.Combinations) {
				return gatewayIssue(compiled.Identity(), protocol.VerificationRequired, "/binding/combinations", "current engine cannot verify this binding; repair its definition or capability contract")
			}
		}
		plan.Bindings = append(plan.Bindings, binding)
	}
	return s.store.RefreshProtocolRuntime(ctx, plan)
}
