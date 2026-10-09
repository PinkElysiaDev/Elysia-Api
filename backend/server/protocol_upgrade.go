package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// protocolUpgradeInput supplies explicit replacements for definitions that
// cannot be imported equivalently. Reports are always computed by the service.
type protocolUpgradeInput struct {
	Baseline    string                    `json:"baseline,omitempty"`
	Definitions map[string]protocol.Value `json:"definitions,omitempty"`
	Bindings    []storage.ProtocolBinding `json:"bindings,omitempty"`
}

type protocolUpgradePreview struct {
	Baseline    string                                 `json:"baseline"`
	Ready       bool                                   `json:"ready"`
	Definitions map[string]protocol.Value              `json:"definitions"`
	Reports     map[string]protocol.VerificationReport `json:"reports"`
	Bindings    []storage.ProtocolBinding              `json:"bindings"`
	Issues      []protocol.ConversionIssue             `json:"issues"`
	plan        storage.ProtocolUpgrade
}

func (s *Server) applyProtocolUpgrade(ctx context.Context, input protocolUpgradeInput) (*storage.ProtocolUpgradeReceipt, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	requestHash := hex.EncodeToString(digest[:])
	previous, err := s.store.ProtocolUpgradeStatus(ctx)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		return matchProtocolUpgradeRequest(previous, requestHash)
	}
	preview, err := s.prepareProtocolUpgrade(ctx, input)
	if errors.Is(err, protocol.ErrRevisionConflict) {
		// Another identical apply may have committed while this request was
		// verifying. Only that exact intent can reuse the completed receipt.
		if previous, readErr := s.store.ProtocolUpgradeStatus(ctx); readErr == nil && previous != nil {
			return matchProtocolUpgradeRequest(previous, requestHash)
		}
	}
	if err != nil {
		return nil, err
	}
	if !preview.Ready {
		return nil, protocol.IssuesError(preview.Issues)
	}
	preview.plan.RequestHash = requestHash
	return s.store.ApplyProtocolUpgrade(ctx, preview.plan)
}

func matchProtocolUpgradeRequest(receipt *storage.ProtocolUpgradeReceipt, requestHash string) (*storage.ProtocolUpgradeReceipt, error) {
	if receipt.RequestHash != requestHash {
		return nil, protocol.ErrRevisionConflict
	}
	return receipt, nil
}

func migrationIssue(id, path, reason string) protocol.ConversionIssue {
	return protocol.ConversionIssue{Code: protocol.InvalidDefinition, Severity: protocol.SeverityError, Protocol: protocol.Identity{DefinitionID: id}, Stage: "migration", Path: path, Reason: reason, Suggestion: "Repair the v2 definition or binding in the preview; rerun verification before applying the whole migration."}
}

// prepareProtocolUpgrade performs no writes. A baseline comparison at both
// ends rejects previews assembled across concurrent configuration changes.
func (s *Server) prepareProtocolUpgrade(ctx context.Context, input protocolUpgradeInput) (*protocolUpgradePreview, error) {
	baseline, err := s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return nil, err
	}
	if input.Baseline != "" && input.Baseline != baseline {
		return nil, protocol.ErrRevisionConflict
	}
	evidence, err := s.store.ProtocolRefreshEvidenceBaseline(ctx)
	if err != nil {
		return nil, err
	}
	service, err := s.protocolService()
	if err != nil {
		return nil, err
	}
	preview := &protocolUpgradePreview{Baseline: baseline, Definitions: map[string]protocol.Value{}, Reports: map[string]protocol.VerificationReport{}, Issues: []protocol.ConversionIssue{}, plan: storage.ProtocolUpgrade{Baseline: baseline, EvidenceBaseline: evidence}}
	drafts, err := s.store.ListProtocolDrafts(ctx)
	if err != nil {
		return nil, err
	}
	savedDrafts := map[string]protocol.Draft{}
	for _, draft := range drafts {
		savedDrafts[draft.ProtocolID] = draft
	}
	active, err := s.store.ListProtocolActivations(ctx)
	if err != nil {
		return nil, err
	}
	for _, activation := range active {
		revision, err := s.store.ReadProtocolRevision(ctx, activation.ProtocolID, activation.RevisionHash)
		if err != nil {
			return nil, err
		}
		preview.Definitions[activation.ProtocolID] = revision.Definition
	}
	shipped, err := builtin.Definitions()
	if err != nil {
		return nil, err
	}
	presets := map[string]protocol.Value{}
	for _, value := range shipped {
		var definition protocol.Definition
		if err := value.Decode(&definition); err != nil {
			return nil, err
		}
		presets[definition.ID] = value
	}
	legacy, err := s.store.ListCustomProtocols(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range legacy {
		if _, exists := preview.Definitions[row.ID]; exists {
			continue
		}
		if preset, exists := presets[row.ID]; exists {
			// 预置只读：无论行是否被改动，一律采用 shipped 版本；被丢弃的
			// 本地修改留审计日志，定制需求走「复制为新协议」。
			preview.Definitions[row.ID] = preset
			if !isUnmodifiedLegacyPreset(row) {
				s.logSystemEvent("info", "preset protocol reset to shipped version; local edits discarded", map[string]any{
					"protocol":      row.ID,
					"discardedHash": presetContentHash(row.Config),
					"discardedSize": len(row.Config),
				})
			}
			continue
		}
		if replacement, exists := input.Definitions[row.ID]; exists {
			preview.Definitions[row.ID] = replacement
			continue
		}
		definition, issues := relay.ImportLegacyProtocol([]byte(row.Config), protocol.CapabilitySet{protocol.TextCapability: true})
		if definition != nil {
			value, err := protocol.EncodeValue(definition)
			if err != nil {
				return nil, err
			}
			preview.Definitions[row.ID] = value
		}
		preview.Issues = append(preview.Issues, issues...)
		// An import is a repair draft. Never infer tool/media promises or replace
		// edited mappings with a preset just because the protocol IDs match.
		preview.Issues = append(preview.Issues, migrationIssue(row.ID, "/definitions/"+row.ID, "edited or custom legacy definition needs an explicitly reviewed v2 replacement"))
	}
	for id, definition := range presets {
		if _, exists := preview.Definitions[id]; !exists {
			preview.Definitions[id] = definition
		}
	}
	for id, definition := range input.Definitions {
		preview.Definitions[id] = definition
	}
	ids := make([]string, 0, len(preview.Definitions))
	for id := range preview.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	compiled := map[string]*protocol.Compiled{}
	for _, id := range ids {
		definition := preview.Definitions[id]
		entry, issues := service.Validate(definition.Bytes())
		preview.Issues = append(preview.Issues, issues...)
		if entry == nil {
			continue
		}
		if entry.Identity().DefinitionID != id {
			preview.Issues = append(preview.Issues, migrationIssue(id, "/definitions/"+id+"/id", "replacement must retain the referenced protocol ID"))
			continue
		}
		report, reportErr := s.store.ReadProtocolReport(ctx, id, entry.Hash())
		if reportErr != nil && !recoverableProtocolRead(reportErr) {
			return nil, reportErr
		}
		if reportErr != nil || protocol.IssuesError(protocol.CanActivate(entry, report)) != nil {
			report = protocol.Verify(ctx, entry)
		}
		preview.Reports[id] = report
		preview.Issues = append(preview.Issues, report.Issues...)
		compiled[id] = entry
		now := time.Now().UTC()
		draft, exists := savedDrafts[id]
		if !exists {
			draft = protocol.Draft{ProtocolID: id, Hash: entry.Hash(), Definition: definition, UpdatedAt: now}
		}
		preview.plan.Revisions = append(preview.plan.Revisions, storage.ProtocolUpgradeRevision{Revision: protocol.Revision{ProtocolID: id, Hash: entry.Hash(), Definition: definition, CreatedAt: now}, Report: report, Draft: draft})
	}
	if err := s.prepareProtocolUpgradeBindings(ctx, input, preview, compiled); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return nil, err
	}
	if current != baseline {
		return nil, protocol.ErrRevisionConflict
	}
	currentEvidence, err := s.store.ProtocolRefreshEvidenceBaseline(ctx)
	if err != nil {
		return nil, err
	}
	if currentEvidence != evidence {
		return nil, protocol.ErrRevisionConflict
	}
	preview.Ready = protocol.IssuesError(preview.Issues) == nil
	return preview, nil
}

func isUnmodifiedLegacyPreset(row storage.CustomProtocol) bool {
	if matchesAnyPresetHash(row.Config, legacyPresetHashes[row.ID]) {
		return true
	}
	preset, exists := findPresetConfig(row.ID)
	if !exists {
		return false
	}
	raw, err := json.Marshal(preset)
	return err == nil && presetContentHash(row.Config) == presetContentHash(string(raw))
}

func protocolIDForPlatform(platform string) (string, error) {
	if strings.HasPrefix(platform, "custom:") {
		return strings.TrimPrefix(platform, "custom:"), nil
	}
	switch platform {
	case "", "openai", "openai_chat", "openai-compatible", "chat_completions":
		return "openai-chat-completions", nil
	case "openai_responses", "responses":
		return "openai-responses", nil
	case "claude", "anthropic":
		return "anthropic-messages", nil
	case "gemini":
		return "google-generate-content", nil
	default:
		return "", fmt.Errorf("platform %q requires an explicit verified protocol binding", platform)
	}
}
