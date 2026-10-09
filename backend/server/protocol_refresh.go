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
	if err != nil {
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
	hasChanges := false
	// ensureProtocol 把一个协议推进到给定（预置=shipped）定义并进入计划。
	// current 为 nil 表示库中尚无激活（预置补激活通道）：等同被替换处理。
	ensureProtocol := func(definitionID string, definition protocol.Value, current *protocol.Revision) error {
		compiled, issues := service.Validate(definition.Bytes())
		if err := protocol.IssuesError(issues); err != nil {
			return err
		}
		revision := protocol.Revision{}
		if current != nil {
			revision = *current
		}
		isReplaced := current == nil || compiled.Hash() != revision.Hash
		report, err := s.store.ReadProtocolReport(ctx, definitionID, compiled.Hash())
		if err != nil && !errors.Is(err, protocol.ErrNotFound) {
			return err
		}
		if err == nil && !report.Passed && report.IsCurrent(compiled.Hash(), protocol.CompilerVersion, compiled.SamplesHash()) && !protocol.IsPresetProtocolID(definitionID) {
			return protocol.IssuesError(protocol.CanActivate(compiled, report))
		}
		if err != nil || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			report = protocol.Verify(ctx, compiled)
			if err := protocol.IssuesError(protocol.CanActivate(compiled, report)); err != nil {
				if current != nil && current.Hash == compiled.Hash() && !protocol.IsPresetProtocolID(definitionID) {
					plan.Rejections = append(plan.Rejections, storage.ProtocolUpgradeRejection{ProtocolID: definitionID, Report: report})
					hasChanges = true
				}
				return err
			}
			hasChanges = true
		}
		draft, err := s.store.ReadProtocolDraft(ctx, definitionID)
		if err != nil && !errors.Is(err, protocol.ErrNotFound) {
			return err
		}
		// 草稿仅在缺失、或预置随版本替换且草稿还停在旧版时重置——
		// 自定义协议的草稿是用户进行中的工作，永不回写覆盖。
		if errors.Is(err, protocol.ErrNotFound) || (isReplaced && draft.Hash == revision.Hash) {
			draft = protocol.Draft{ProtocolID: definitionID, Hash: compiled.Hash(), Definition: definition, UpdatedAt: time.Now().UTC()}
		}
		if isReplaced {
			revision = protocol.Revision{ProtocolID: definitionID, Hash: compiled.Hash(), Definition: definition, CreatedAt: time.Now().UTC()}
		}
		hasChanges = hasChanges || isReplaced
		plan.Revisions = append(plan.Revisions, storage.ProtocolUpgradeRevision{Revision: revision, Report: report, Draft: draft})
		definitions[definitionID] = compiled
		return nil
	}
	for _, activation := range active {
		revision, err := s.store.ReadProtocolRevision(ctx, activation.ProtocolID, activation.RevisionHash)
		if err != nil {
			if definition, preset := presets[activation.ProtocolID]; preset {
				if err := ensureProtocol(activation.ProtocolID, definition, nil); err != nil {
					return err
				}
			}
			continue
		}
		definition := revision.Definition
		// 预置只读：启动时无条件跟进 shipped 版本，本地激活的旧/改版本被
		// 替换并走下方重验链（hash 未变时为无操作）。
		if replacement, exists := presets[activation.ProtocolID]; exists {
			definition = replacement
		}
		if err := ensureProtocol(activation.ProtocolID, definition, &revision); err != nil {
			if protocol.IsPresetProtocolID(activation.ProtocolID) {
				return err
			}
			// Retain activation intent and original data; ReloadAvailable exposes
			// this custom revision's failure without blocking healthy protocols.
			continue
		}
	}
	// 自愈通道：预置属引擎所有，激活是「随版本自动更新」策略的蕴含——
	// 任何混合状态（改名对被跳过/历史半途启动）导致预置缺激活时，这里
	// 直接补上，避免 Pin 永远失败、源拉取报 verification_required。
	for _, id := range sortedDefinitionIDs(presets) {
		if _, active := definitions[id]; active {
			continue
		}
		if err := ensureProtocol(id, presets[id], nil); err != nil {
			return err
		}
	}
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if definitions[binding.Binding.ProtocolID] == nil {
			continue
		}
		if !binding.Unbound && binding.Kind != "group" && len(binding.Combinations) == 0 {
			hasChanges = true
		}
		for _, report := range binding.Combinations {
			if report.CompilerVersion != protocol.CompilerVersion {
				hasChanges = true
			}
		}
	}
	if !hasChanges {
		return nil
	}
	for _, binding := range bindings {
		compiled := definitions[binding.Binding.ProtocolID]
		if binding.Unbound {
			plan.Bindings = append(plan.Bindings, binding)
			continue
		}
		if compiled == nil {
			continue
		} // Keep the unavailable binding unchanged.
		binding.Binding.RevisionHash = compiled.Hash()
		plan.Bindings = append(plan.Bindings, binding)
	}
	policies, _, _, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return err
	}
	plan.Bindings, err = s.verifyConversionBindings(ctx, conversionDefinitions(definitions), policies, plan.Bindings, true)
	if err != nil {
		return err
	}
	return s.store.RefreshProtocolRuntime(ctx, plan)
}

func sortedDefinitionIDs(definitions map[string]protocol.Value) []string {
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
