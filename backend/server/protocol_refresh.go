package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

var requiredPresetIDs = []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID}

func (s *Server) refreshProtocolRuntime(ctx context.Context, service *protocol.Service) error {
	return s.refreshProtocolRuntimeScope(ctx, service, false)
}

// One recovery path serves startup and reload. The first pass reads only current
// records; a changing plan is rebuilt under a baseline before it can commit.
// Healthy restarts never fingerprint historical definitions or create backups.
func (s *Server) refreshProtocolRuntimeScope(ctx context.Context, service *protocol.Service, presetsOnly bool) error {
	_, _, changed, err := s.prepareProtocolRefresh(ctx, service, presetsOnly)
	if err != nil || !changed {
		return err
	}
	baseline, err := s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return err
	}
	evidence, err := s.store.ProtocolRefreshEvidenceBaseline(ctx)
	if err != nil {
		return err
	}
	plan, definitions, changed, err := s.prepareProtocolRefresh(ctx, service, presetsOnly)
	if err != nil || !changed {
		return err
	}
	plan.Baseline, plan.EvidenceBaseline = baseline, evidence
	if len(plan.Bindings) > 0 {
		policies, bindings, _, err := s.store.ConversionSnapshot(ctx)
		if err != nil {
			return err
		}
		// Verification needs the entire inheritance chain, even when only one
		// binding is being refreshed. Persist only the affected bindings.
		selected := map[string]bool{}
		before := map[string]string{}
		key := func(b storage.ProtocolBinding) string {
			raw, _ := json.Marshal([]string{b.Kind, b.SourceID, b.ModelID, b.GroupID})
			return string(raw)
		}
		for _, b := range plan.Bindings {
			selected[key(b)] = true
		}
		for i, b := range bindings {
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}
			before[key(b)] = string(raw)
			if selected[key(b)] {
				bindings[i].Binding.RevisionHash = definitions[b.Binding.ProtocolID].Hash()
			}
		}
		verified, err := s.verifyConversionBindings(ctx, conversionDefinitions(definitions), policies, bindings, true)
		if err != nil {
			return err
		}
		plan.Bindings = nil
		for _, b := range verified {
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}
			if selected[key(b)] && before[key(b)] != string(raw) {
				plan.Bindings = append(plan.Bindings, b)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.store.RefreshProtocolRuntime(ctx, plan); err != nil {
		return fmt.Errorf("protocol recovery commit: %w", err)
	}
	for _, entry := range plan.Revisions {
		if protocol.IsPresetProtocolID(entry.Revision.ProtocolID) && entry.Refresh.Changed() {
			log.Printf("[protocol-recovery] %s restored: revision=%t evidence=%t activation=%t draft=%t", entry.Revision.ProtocolID, entry.Refresh.Revision || entry.Refresh.RepairPreset, entry.Refresh.Report, entry.Refresh.Activation, entry.Refresh.Draft)
		}
	}
	return nil
}

func recoverableProtocolRead(err error) bool {
	return errors.Is(err, protocol.ErrNotFound) || errors.Is(err, storage.ErrCorruptProtocolRecord)
}

func (s *Server) prepareProtocolRefresh(ctx context.Context, service *protocol.Service, presetsOnly bool) (storage.ProtocolUpgrade, map[string]*protocol.Compiled, bool, error) {
	plan := storage.ProtocolUpgrade{}
	definitions := map[string]*protocol.Compiled{}
	fail := func(err error) (storage.ProtocolUpgrade, map[string]*protocol.Compiled, bool, error) {
		return plan, definitions, false, err
	}
	active, err := s.store.ListProtocolActivations(ctx)
	if err != nil {
		return fail(err)
	}
	activations := map[string]protocol.Activation{}
	for _, a := range active {
		activations[a.ProtocolID] = a
	}
	shipped, err := builtin.Definitions()
	if err != nil {
		return fail(err)
	}
	changed := false
	ensure := func(id string, value protocol.Value, preset bool) error {
		compiled, issues := service.Validate(value.Bytes())
		if err := protocol.IssuesError(issues); err != nil {
			return fmt.Errorf("protocol %s compile: %w", id, err)
		}
		if compiled.Identity().DefinitionID != id {
			return fmt.Errorf("%w: protocol %s identity mismatch", storage.ErrCorruptProtocolRecord, id)
		}
		a, isActive := activations[id]
		if !preset && a.RevisionHash != compiled.Hash() {
			return fmt.Errorf("%w: protocol %s stored hash mismatch", storage.ErrCorruptProtocolRecord, id)
		}
		writes := &storage.ProtocolRefreshWrite{}
		revision, err := s.store.ReadProtocolRevision(ctx, id, compiled.Hash())
		if err != nil && !recoverableProtocolRead(err) {
			return err
		}
		if errors.Is(err, protocol.ErrNotFound) {
			writes.Revision = true
		} else if errors.Is(err, storage.ErrCorruptProtocolRecord) {
			writes.RepairPreset = preset
		} else if preset {
			stored, issues := service.Validate(revision.Definition.Bytes())
			writes.RepairPreset = protocol.IssuesError(issues) != nil || stored == nil || stored.Identity().DefinitionID != id || stored.Hash() != compiled.Hash()
		}
		if writes.Revision || writes.RepairPreset {
			revision = protocol.Revision{ProtocolID: id, Hash: compiled.Hash(), Definition: value, CreatedAt: time.Now().UTC()}
		}
		writes.Activation = !isActive || a.RevisionHash != compiled.Hash() || a.Generation < 1 || a.ActivatedAt.IsZero()
		report, err := s.store.ReadProtocolReport(ctx, id, compiled.Hash())
		if err != nil && !recoverableProtocolRead(err) {
			return err
		}
		if err == nil && !preset && !report.Passed && report.IsCurrent(compiled.Hash(), protocol.CompilerVersion, compiled.SamplesHash()) {
			return protocol.IssuesError(protocol.CanActivate(compiled, report))
		}
		if err != nil || writes.RepairPreset || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			report = protocol.Verify(ctx, compiled)
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := protocol.IssuesError(protocol.CanActivate(compiled, report)); err != nil {
				if !preset {
					plan.Rejections = append(plan.Rejections, storage.ProtocolUpgradeRejection{ProtocolID: id, Report: report})
					changed = true
				}
				return fmt.Errorf("protocol %s offline verification: %w", id, err)
			}
			writes.Report = true
		}
		draft, err := s.store.ReadProtocolDraft(ctx, id)
		if err != nil && !recoverableProtocolRead(err) {
			return err
		}
		if err != nil && !preset && !errors.Is(err, protocol.ErrNotFound) {
			return err
		}
		// Preserve unrelated operator drafts. A corrupt preset draft or a draft
		// still following the replaced preset is archived before replacement.
		damagedDraft := false
		if err == nil && preset && draft.Hash == compiled.Hash() && !storage.EquivalentProtocolDefinition(draft.Definition, value) {
			// Omitted defaults and explicitly encoded defaults are both valid.
			// Only identity/hash corruption warrants replacing a current draft.
			draftCompiled, issues := service.Validate(draft.Definition.Bytes())
			damagedDraft = protocol.IssuesError(issues) != nil || draftCompiled == nil || draftCompiled.Hash() != draft.Hash || draftCompiled.Identity().DefinitionID != id
		}
		writes.Draft = err != nil || (preset && ((a.RevisionHash != compiled.Hash() && draft.Hash == a.RevisionHash) || damagedDraft))
		if writes.Draft {
			draft = protocol.Draft{ProtocolID: id, Hash: compiled.Hash(), Definition: value, UpdatedAt: time.Now().UTC()}
		}
		plan.Revisions = append(plan.Revisions, storage.ProtocolUpgradeRevision{Revision: revision, Report: report, Draft: draft, Refresh: writes})
		definitions[id] = compiled
		changed = changed || writes.Changed()
		return nil
	}
	// Presets always start from embedded content, never from damaged stored data.
	for _, value := range shipped {
		var d protocol.Definition
		if err := value.Decode(&d); err != nil {
			return fail(err)
		}
		if err := ensure(d.ID, value, true); err != nil {
			return fail(fmt.Errorf("preset %s recovery: %w", d.ID, err))
		}
	}
	for _, id := range requiredPresetIDs {
		if definitions[id] == nil {
			return fail(fmt.Errorf("preset %s is missing from embedded definitions", id))
		}
	}
	if !presetsOnly {
		for _, a := range active {
			if protocol.IsPresetProtocolID(a.ProtocolID) {
				continue
			}
			r, err := s.store.ReadProtocolRevision(ctx, a.ProtocolID, a.RevisionHash)
			if err != nil {
				if !recoverableProtocolRead(err) {
					return fail(err)
				}
				continue
			}
			if err := ensure(a.ProtocolID, r.Definition, false); err != nil {
				if ctx.Err() != nil {
					return fail(ctx.Err())
				}
				var conversion *protocol.ConversionError
				if !errors.As(err, &conversion) && !errors.Is(err, storage.ErrCorruptProtocolRecord) {
					return fail(err)
				}
				log.Printf("[protocol-refresh] custom %s unavailable: %v", a.ProtocolID, err)
			}
		}
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return fail(err)
	}
	for _, b := range bindings {
		c := definitions[b.Binding.ProtocolID]
		if b.Unbound || c == nil {
			continue
		}
		if !bindingNeedsProtocolRefresh(b, definitions) {
			continue
		}
		changed = true
		b.Binding.RevisionHash = c.Hash()
		plan.Bindings = append(plan.Bindings, b)
	}
	return plan, definitions, changed, nil
}

func bindingNeedsProtocolRefresh(b storage.ProtocolBinding, definitions map[string]*protocol.Compiled) bool {
	if b.Binding.RevisionHash != definitions[b.Binding.ProtocolID].Hash() {
		return true
	}
	if b.Kind == "group" {
		return false
	}
	if len(b.Combinations) == 0 {
		return true
	}
	covered := map[string]bool{}
	for _, r := range b.Combinations {
		if r.CompilerVersion != protocol.CompilerVersion {
			return true
		}
		// A current whole-binding rejection is itself stable evidence. A
		// capability/operation change is checked by its management write path.
		if r.SourceHash == "" && r.TargetHash == "" && r.Fidelity == "rejected" {
			return false
		}
		if r.TargetHash != definitions[b.Binding.ProtocolID].Hash() {
			return true
		}
		covered[r.SourceHash] = true
	}
	for _, c := range definitions {
		if c.Supports(protocol.DecodeRequest) && c.Supports(protocol.EncodeResponse) && !covered[c.Hash()] {
			return true
		}
	}
	return false
}

func sortedDefinitionIDs(definitions map[string]protocol.Value) []string {
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
