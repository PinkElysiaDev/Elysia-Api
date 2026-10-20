package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
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
		selected := map[storage.BindingKey]bool{}
		before := map[storage.BindingKey]string{}
		for _, binding := range plan.Bindings {
			selected[binding.Key()] = true
		}
		scope := conversionBindingScope{keys: selected, isolate: true}
		for i, b := range bindings {
			raw, err := sonic.ConfigStd.Marshal(b)
			if err != nil {
				return err
			}
			before[b.Key()] = string(raw)
			if scope.includes(b.Key()) {
				if compiled := definitions[b.Binding.ProtocolID]; compiled != nil && !b.Unbound {
					bindings[i].Binding.RevisionHash = compiled.Hash()
				}
			}
		}
		verified, err := s.verifyConversionBindings(ctx, conversionDefinitions(definitions), policies, bindings, scope)
		if err != nil {
			return err
		}
		plan.Bindings = nil
		for _, b := range verified {
			raw, err := sonic.ConfigStd.Marshal(b)
			if err != nil {
				return err
			}
			if scope.includes(b.Key()) && before[b.Key()] != string(raw) {
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
			s.logEvent("info").Str("protocol_id", entry.Revision.ProtocolID).Bool("revision", entry.Refresh.Revision || entry.Refresh.RepairPreset).Bool("evidence", entry.Refresh.Report).Bool("activation", entry.Refresh.Activation).Bool("draft", entry.Refresh.Draft).Msg("protocol restored")
		}
	}
	return nil
}

func recoverableProtocolRead(err error) bool {
	return errors.Is(err, protocol.ErrNotFound) || errors.Is(err, storage.ErrCorruptProtocolRecord)
}

type protocolRefreshPlan struct {
	server      *Server
	context     context.Context
	service     *protocol.Service
	upgrade     storage.ProtocolUpgrade
	definitions map[string]*protocol.Compiled
	activations map[string]protocol.Activation
	hasChanges  bool
}

func (plan *protocolRefreshPlan) ensureProtocol(id string, value protocol.Value, isPreset bool) error {
	compiled, issues := plan.service.Validate(value.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		return fmt.Errorf("protocol %s compile: %w", id, err)
	}
	if compiled.Identity().DefinitionID != id {
		return fmt.Errorf("%w: protocol %s identity mismatch", storage.ErrCorruptProtocolRecord, id)
	}
	a, isActive := plan.activations[id]
	if !isPreset && a.RevisionHash != compiled.Hash() {
		return fmt.Errorf("%w: protocol %s stored hash mismatch", storage.ErrCorruptProtocolRecord, id)
	}
	writes := &storage.ProtocolRefreshWrite{}
	revision, err := plan.server.store.ReadProtocolRevision(plan.context, id, compiled.Hash())
	if err != nil && !recoverableProtocolRead(err) {
		return err
	}
	if errors.Is(err, protocol.ErrNotFound) {
		writes.Revision = true
	} else if errors.Is(err, storage.ErrCorruptProtocolRecord) {
		writes.RepairPreset = isPreset
	} else if isPreset {
		stored, issues := plan.service.Validate(revision.Definition.Bytes())
		writes.RepairPreset = protocol.IssuesError(issues) != nil || stored == nil || stored.Identity().DefinitionID != id || stored.Hash() != compiled.Hash()
	}
	if writes.Revision || writes.RepairPreset {
		revision = protocol.Revision{ProtocolID: id, Hash: compiled.Hash(), Definition: value, CreatedAt: time.Now().UTC()}
	}
	writes.Activation = !isActive || a.RevisionHash != compiled.Hash() || a.Generation < 1 || a.ActivatedAt.IsZero()
	report, err := plan.server.store.ReadProtocolReport(plan.context, id, compiled.Hash())
	if err != nil && !recoverableProtocolRead(err) {
		return err
	}
	if err == nil && !isPreset && !report.Passed && report.IsCurrent(compiled.Hash(), protocol.CompilerVersion, compiled.SamplesHash()) {
		return protocol.IssuesError(protocol.CanActivate(compiled, report))
	}
	if err != nil || writes.RepairPreset || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
		report = protocol.Verify(plan.context, compiled)
		if err := plan.context.Err(); err != nil {
			return err
		}
		if err := protocol.IssuesError(protocol.CanActivate(compiled, report)); err != nil {
			if !isPreset {
				plan.upgrade.Rejections = append(plan.upgrade.Rejections, storage.ProtocolUpgradeRejection{ProtocolID: id, Report: report})
				plan.hasChanges = true
			}
			return fmt.Errorf("protocol %s offline verification: %w", id, err)
		}
		writes.Report = true
	}
	draft, err := plan.server.store.ReadProtocolDraft(plan.context, id)
	if err != nil && !recoverableProtocolRead(err) {
		return err
	}
	if err != nil && !isPreset && !errors.Is(err, protocol.ErrNotFound) {
		return err
	}
	// Preserve unrelated operator drafts. A corrupt preset draft or a draft
	// still following the replaced preset is archived before replacement.
	damagedDraft := false
	if err == nil && isPreset && draft.Hash == compiled.Hash() && !storage.EquivalentProtocolDefinition(draft.Definition, value) {
		// Omitted defaults and explicitly encoded defaults are both valid.
		// Only identity/hash corruption warrants replacing a current draft.
		draftCompiled, issues := plan.service.Validate(draft.Definition.Bytes())
		damagedDraft = protocol.IssuesError(issues) != nil || draftCompiled == nil || draftCompiled.Hash() != draft.Hash || draftCompiled.Identity().DefinitionID != id
	}
	writes.Draft = err != nil || (isPreset && ((a.RevisionHash != compiled.Hash() && draft.Hash == a.RevisionHash) || damagedDraft))
	if writes.Draft {
		draft = protocol.Draft{ProtocolID: id, Hash: compiled.Hash(), Definition: value, UpdatedAt: time.Now().UTC()}
	}
	plan.upgrade.Revisions = append(plan.upgrade.Revisions, storage.ProtocolUpgradeRevision{Revision: revision, Report: report, Draft: draft, Refresh: writes})
	plan.definitions[id] = compiled
	plan.hasChanges = plan.hasChanges || writes.Changed()
	return nil
}

func (s *Server) prepareProtocolRefresh(ctx context.Context, service *protocol.Service, presetsOnly bool) (storage.ProtocolUpgrade, map[string]*protocol.Compiled, bool, error) {
	plan := &protocolRefreshPlan{server: s, context: ctx, service: service, definitions: map[string]*protocol.Compiled{}}
	fail := func(err error) (storage.ProtocolUpgrade, map[string]*protocol.Compiled, bool, error) {
		return plan.upgrade, plan.definitions, false, err
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
	plan.activations = activations
	// Presets always start from embedded content, never from damaged stored data.
	for _, value := range shipped {
		var d protocol.Definition
		if err := value.Decode(&d); err != nil {
			return fail(err)
		}
		if err := plan.ensureProtocol(d.ID, value, true); err != nil {
			return fail(fmt.Errorf("preset %s recovery: %w", d.ID, err))
		}
	}
	for _, id := range requiredPresetIDs {
		if plan.definitions[id] == nil {
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
			if err := plan.ensureProtocol(a.ProtocolID, r.Definition, false); err != nil {
				if ctx.Err() != nil {
					return fail(ctx.Err())
				}
				var conversion *protocol.ConversionError
				if !errors.As(err, &conversion) && !errors.Is(err, storage.ErrCorruptProtocolRecord) {
					return fail(err)
				}
				s.logEvent("error").Str("protocol_id", a.ProtocolID).Err(err).Msg("custom protocol unavailable")
			}
		}
	}
	// Missing presets can be committed independently of custom protocol bindings.
	if presetsOnly {
		return plan.upgrade, plan.definitions, plan.hasChanges, nil
	}
	policies, bindings, _, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return fail(err)
	}
	models, err := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		return fail(err)
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return fail(err)
	}
	sourceKeys := collectSourceKeys(sources)
	for _, b := range bindings {
		c := plan.definitions[b.Binding.ProtocolID]
		if b.Unbound || c == nil {
			continue
		}
		needsRefresh := bindingNeedsProtocolRefresh(b, plan.definitions)
		if !needsRefresh && b.Kind != "group" {
			checks, err := s.resolveBindingConversions(ctx, conversionDefinitions(plan.definitions), policies, bindings, b, s.bindingModelReferences(b, models, sourceKeys))
			if err != nil {
				if ctx.Err() != nil {
					return fail(ctx.Err())
				}
				// Reuse an identical current rejection, but retry when its cause changes.
				needsRefresh = !hasCurrentBindingRejection(b, err)
			} else {
				needsRefresh = !hasBindingConversionEvidence(b, checks)
			}
		}
		if !needsRefresh {
			continue
		}
		plan.hasChanges = true
		b.Binding.RevisionHash = c.Hash()
		plan.upgrade.Bindings = append(plan.upgrade.Bindings, b)
	}
	return plan.upgrade, plan.definitions, plan.hasChanges, nil
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
