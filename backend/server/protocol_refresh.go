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
		if err != nil || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			report = protocol.Verify(ctx, compiled)
			if err := protocol.IssuesError(protocol.CanActivate(compiled, report)); err != nil {
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
		ids = append(ids, definitionID)
		return nil
	}
	for _, activation := range active {
		revision, err := s.store.ReadProtocolRevision(ctx, activation.ProtocolID, activation.RevisionHash)
		if err != nil {
			return err
		}
		definition := revision.Definition
		// 预置只读：启动时无条件跟进 shipped 版本，本地激活的旧/改版本被
		// 替换并走下方重验链（hash 未变时为无操作）。
		if replacement, exists := presets[activation.ProtocolID]; exists {
			definition = replacement
		}
		if err := ensureProtocol(activation.ProtocolID, definition, &revision); err != nil {
			return err
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
		if binding.Unbound {
			plan.Bindings = append(plan.Bindings, binding)
			continue
		}
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
	policies, _, _, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return err
	}
	configured := len(policies) > 0
	for _, b := range plan.Bindings {
		configured = configured || b.Conversion != nil
	}
	if configured {
		plan.Bindings, err = s.verifyConversionBindings(ctx, conversionDefinitions(definitions), policies, plan.Bindings)
		if err != nil {
			return err
		}
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
