package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) adminProtocolHistory(ctx context.Context, c *app.RequestContext) {
	items, err := s.store.ListProtocolHistory(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	for index := range items {
		items[index].Definition = protocol.Value{}
		items[index].RawDefinition = ""
	}
	respondOK(ctx, c, utils.H{"items": items})
}

func (s *Server) adminProtocolHistoryDetail(ctx context.Context, c *app.RequestContext) {
	item, err := s.store.ReadProtocolHistory(ctx, c.Param("archiveId"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	references, err := s.store.ProtocolRevisionReferences(ctx, item.ProtocolID, item.Hash)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	references = append(references, s.protocolUses.references(item.ProtocolID, item.Hash)...)
	report, err := s.store.ReadProtocolReport(ctx, item.ProtocolID, item.Hash)
	if err != nil && !errors.Is(err, protocol.ErrNotFound) && !errors.Is(err, storage.ErrCorruptProtocolRecord) {
		respondProtocolError(ctx, c, err)
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	result := utils.H{"item": item, "references": references, "report": report}
	if errors.Is(err, storage.ErrCorruptProtocolRecord) {
		result["reportError"] = err.Error()
	}
	// A repaired same-hash row now has fresh evidence. It is not evidence for
	// the damaged archived bytes.
	if item.Reason == "preset_repaired" || item.Reason == "preset_draft_replaced" {
		delete(result, "report")
	}
	if active, found := service.Pin(item.ProtocolID); found {
		result["currentHash"] = active.Hash()
		if changes, err := service.Diff(ctx, item.ProtocolID, item.Hash, active.Hash()); err == nil {
			result["changes"] = changes
		}
	}
	respondOK(ctx, c, result)
}

func (s *Server) adminRestoreProtocolHistory(ctx context.Context, c *app.RequestContext) {
	var input struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	item, err := s.store.ReadProtocolHistory(ctx, c.Param("archiveId"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if strings.TrimSpace(input.ID) == item.ProtocolID {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_restore_id", "恢复必须使用新的协议 ID")
		return
	}
	if item.ReadError != "" {
		respondFail(ctx, c, http.StatusBadRequest, "corrupt_archive", "归档保留了损坏原文，请先修正定义再验证："+item.ReadError)
		return
	}
	fields, err := item.Definition.ReadObject()
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	fields["id"] = protocol.StringValue(strings.TrimSpace(input.ID))
	if strings.TrimSpace(input.Name) != "" {
		fields["name"] = protocol.StringValue(strings.TrimSpace(input.Name))
	}
	definition, err := protocol.EncodeValue(fields)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	draft, issues, err := service.SaveDraft(ctx, strings.TrimSpace(input.ID), definition.Bytes(), "")
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	result := utils.H{"protocolId": draft.ProtocolID, "activated": false, "issues": issues}
	if protocol.IssuesError(issues) != nil {
		respondOK(ctx, c, result)
		return
	}
	revision, report, err := service.VerifyDraft(ctx, draft.ProtocolID, draft.Hash)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	result["report"], result["issues"] = report, report.Issues
	if !report.Passed {
		respondOK(ctx, c, result)
		return
	}
	activation, err := service.Activate(ctx, draft.ProtocolID, revision.Hash, "")
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	s.invalidateRouteCache()
	result["activated"], result["activation"] = true, activation
	respondOK(ctx, c, result)
}

func (s *Server) adminDeleteProtocolHistory(ctx context.Context, c *app.RequestContext) {
	item, err := s.store.ReadProtocolHistory(ctx, c.Param("archiveId"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	// 预置只读：preset_replaced 历史行是引擎更新的审计痕迹，不提供物理删除。
	if protocol.IsPresetProtocolID(item.ProtocolID) {
		respondFail(ctx, c, http.StatusBadRequest, "preset_readonly", "预置协议只读，历史版本随引擎更新保留")
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	err = s.protocolUses.exclusive(item.ProtocolID, item.Hash, func() error {
		return service.DeleteRetained(item.ProtocolID, item.Hash, func() error { return s.store.DeleteProtocolHistory(ctx, item.ID) })
	})
	if err != nil {
		if errors.Is(err, protocol.ErrRevisionConflict) {
			refs, lookupErr := s.store.ProtocolRevisionReferences(ctx, item.ProtocolID, item.Hash)
			if lookupErr == nil {
				refs = append(refs, s.protocolUses.references(item.ProtocolID, item.Hash)...)
				c.JSON(http.StatusConflict, utils.H{"ok": false, "error": utils.H{"code": "protocol_in_use", "message": "历史版本仍被引用，请解除依赖后重试", "references": refs}})
				return
			}
		}
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"deleted": true})
}

type protocolReferenceSnapshot struct {
	Baseline       string                      `json:"baseline"`
	References     []storage.ProtocolReference `json:"references"`
	AffectedModels []storage.ProtocolReference `json:"affectedModels"`
	bindings       []storage.ProtocolBinding
	sources        []storage.ModelSource
	models         []storage.Model
}

func (s *Server) protocolReferences(ctx context.Context, id string) (protocolReferenceSnapshot, error) {
	result := protocolReferenceSnapshot{References: []storage.ProtocolReference{}, AffectedModels: []storage.ProtocolReference{}}
	var err error
	result.Baseline, err = s.store.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return result, err
	}
	result.bindings, err = s.store.ListProtocolBindings(ctx)
	if err != nil {
		return result, err
	}
	result.sources, err = s.store.ListSources(ctx)
	if err != nil {
		return result, err
	}
	result.models, err = s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	add := func(ref storage.ProtocolReference) {
		key := ref.Kind + "\x00" + ref.SourceID + "\x00" + ref.ID
		if !seen[key] {
			result.References = append(result.References, ref)
			seen[key] = true
		}
	}
	for _, binding := range result.bindings {
		if !binding.Unbound && binding.Binding.ProtocolID == id {
			ref := storage.ProtocolReference{Kind: binding.Kind, SourceID: binding.SourceID, ID: binding.ModelID}
			if binding.Kind == "group" {
				ref.ID = binding.GroupID
			}
			add(ref)
		}
	}
	for _, source := range result.sources {
		if strings.EqualFold(source.Platform, "custom:"+id) {
			add(storage.ProtocolReference{Kind: "source", SourceID: source.ID, Name: source.Name})
		}
	}
	for _, model := range result.models {
		entry, found := selectProtocolBinding(result.bindings, modelReference(model))
		if strings.EqualFold(model.Platform, "custom:"+id) {
			add(storage.ProtocolReference{Kind: "model", SourceID: model.SourceID, ID: model.ID, Name: model.Name})
		}
		if (found && !entry.Unbound && entry.Binding.ProtocolID == id) || strings.EqualFold(model.Platform, "custom:"+id) {
			result.AffectedModels = append(result.AffectedModels, storage.ProtocolReference{Kind: "model", SourceID: model.SourceID, ID: model.ID, Name: model.Name})
		}
	}
	return result, nil
}

func (s *Server) adminProtocolReferences(ctx context.Context, c *app.RequestContext) {
	result, err := s.protocolReferences(ctx, c.Param("id"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, result)
}

func (s *Server) adminArchiveProtocol(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Mode             string `json:"mode"`
		TargetProtocolID string `json:"targetProtocolId"`
		Baseline         string `json:"baseline"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if input.Mode == "" {
		input.Mode = "block"
	}
	if input.Mode != "block" && input.Mode != "replace" && input.Mode != "unbind" {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_mode", "请选择替换协议或取消绑定")
		return
	}
	id := c.Param("id")
	if protocol.IsPresetProtocolID(id) {
		respondFail(ctx, c, http.StatusBadRequest, "preset_readonly", "当前预置协议不能删除")
		return
	}
	snapshot, err := s.protocolReferences(ctx, id)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if input.Baseline == "" || input.Baseline != snapshot.Baseline {
		respondProtocolError(ctx, c, protocol.ErrRevisionConflict)
		return
	}
	if input.Mode == "block" && len(snapshot.References) > 0 {
		c.JSON(http.StatusConflict, utils.H{"ok": false, "error": utils.H{"code": "protocol_in_use", "message": "请先替换协议或取消绑定", "references": snapshot.References}})
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	view := service.View()
	var target *protocol.Compiled
	replacement := ""
	if input.Mode == "replace" {
		if input.TargetProtocolID == id {
			respondFail(ctx, c, http.StatusBadRequest, "invalid_target", "替换目标必须是其他已启用协议")
			return
		}
		target, _ = view.Pin(input.TargetProtocolID)
		if target == nil {
			respondFail(ctx, c, http.StatusBadRequest, "invalid_target", "替换目标协议未启用")
			return
		}
		replacement = "custom:" + input.TargetProtocolID
		for _, source := range snapshot.sources {
			if strings.EqualFold(source.Platform, "custom:"+id) && source.AutoFetchModels {
				if _, _, err := selectModelDiscovery(target); err != nil {
					respondProtocolError(ctx, c, err)
					return
				}
			}
		}
	}
	updates := []storage.ProtocolBinding{}
	for _, ref := range snapshot.References {
		var entry storage.ProtocolBinding
		found := false
		for _, b := range snapshot.bindings {
			if b.Kind == ref.Kind && b.SourceID == ref.SourceID && ((ref.Kind == "source") || (ref.Kind == "model" && b.ModelID == ref.ID) || (ref.Kind == "group" && b.GroupID == ref.ID)) {
				entry, found = b, true
				break
			}
		}
		// A compatibility platform label may still name the old protocol even
		// while an explicit binding overrides it. Preserve that independent contract.
		if found && (entry.Unbound || entry.Binding.ProtocolID != id) {
			updates = append(updates, entry)
			continue
		}
		if !found {
			entry = storage.ProtocolBinding{Kind: ref.Kind, SourceID: ref.SourceID}
			if ref.Kind == "model" {
				entry.ModelID = ref.ID
			}
			if ref.Kind == "group" {
				entry.GroupID = ref.ID
			}
		}
		if input.Mode == "unbind" {
			entry.Unbound = true
			entry.Binding = protocol.Binding{}
			entry.Combinations = nil
		} else if target != nil {
			if !found {
				entry = makeProtocolBinding(entry.Key(), target)
				if entry.Kind == "model" {
					for _, model := range snapshot.models {
						if model.SourceID == entry.SourceID && model.ID == entry.ModelID {
							entry.Binding.Capabilities = maps.Clone(entry.Binding.Capabilities)
							for cap := range entry.Binding.Capabilities {
								if (!model.ToolsCapable && strings.HasPrefix(string(cap), "tools.")) || (!model.VisionCapable && (cap == protocol.ImagesCapability || cap == protocol.AudioCapability || cap == protocol.VideoCapability)) {
									delete(entry.Binding.Capabilities, cap)
								}
							}
						}
					}
				}
			}
			entry.Binding.ProtocolID, entry.Binding.RevisionHash = input.TargetProtocolID, target.Hash()
			issues := protocol.CheckBinding(entry.Binding, target)
			if entry.Kind == "group" {
				issues = protocol.CheckIngressBinding(entry.Binding, target)
			}
			if err := protocol.IssuesError(issues); err != nil {
				respondProtocolError(ctx, c, err)
				return
			}
			if err := s.checkProtocolBindingTarget(ctx, entry); err != nil {
				respondProtocolError(ctx, c, err)
				return
			}
			if entry.Kind != "group" {
				entry.Combinations, err = s.verifyGatewayBinding(ctx, view, entry, id)
				if err != nil {
					respondProtocolError(ctx, c, err)
					return
				}
				if !hasPassingGatewayCombination(entry.Combinations) {
					respondProtocolError(ctx, c, fmt.Errorf("替换协议未通过入口兼容性验证"))
					return
				}
			}
		}
		updates = append(updates, entry)
	}
	err = service.RemoveActive(id, func() error { return s.store.ArchiveProtocol(ctx, id, input.Baseline, replacement, updates) })
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	s.invalidateRouteCache()
	respondOK(ctx, c, utils.H{"archived": true})
}
