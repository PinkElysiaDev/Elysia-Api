package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) adminProtocolBindings(ctx context.Context, c *app.RequestContext) {
	bindings, err := s.store.ListProtocolBindings(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, bindings)
}

func (s *Server) adminSaveProtocolBinding(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	var binding storage.ProtocolBinding
	if err := decodeProtocolAdminBody(ctx, c, &binding); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	policies, bindings, generation, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if binding.Conversion != nil && binding.Conversion.PolicyID != "" {
		revisions, e := s.store.ConversionRevisions(ctx, binding.Conversion.PolicyID)
		if e != nil {
			respondProtocolError(ctx, c, e)
			return
		}
		for _, r := range revisions {
			if r.Hash == binding.Conversion.RevisionHash {
				if r.CompilerVersion != protocol.CompilerVersion {
					respondFail(ctx, c, http.StatusBadRequest, "verification_required", "conversion revision requires current verification")
					return
				}
				policies = append(policies, r)
				break
			}
		}
	}
	if binding.Unbound {
		binding.Binding = protocol.Binding{}
		binding.Combinations = nil
	} else {
		compiled, _ := service.Pin(binding.Binding.ProtocolID)
		issues := protocol.CheckBinding(binding.Binding, compiled)
		if binding.Kind == "group" {
			issues = protocol.CheckIngressBinding(binding.Binding, compiled)
		}
		if err := protocol.IssuesError(issues); err != nil {
			respondProtocolError(ctx, c, err)
			return
		}
	}
	if err := s.checkProtocolBindingTarget(ctx, binding); err != nil {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_binding", err.Error())
		return
	}
	updates, err := s.prepareBindingUpdate(ctx, service.View(), policies, bindings, binding)
	if err != nil {
		respondFail(ctx, c, http.StatusBadRequest, "incompatible_binding", err.Error())
		return
	}
	if err := s.store.SaveManagedProtocolBindings(ctx, binding.Key(), updates, generation); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	for _, updated := range updates {
		if updated.Key() == binding.Key() {
			binding = updated
			break
		}
	}
	s.invalidateRouteCache()
	respondOK(ctx, c, binding)
}

func (s *Server) checkProtocolBindingTarget(ctx context.Context, binding storage.ProtocolBinding) error {
	groups := s.getGroups()
	if binding.Kind == "group" {
		for _, group := range groups {
			if group.ID != binding.GroupID {
				continue
			}
			canUseTools := group.ToolsCapable == nil || *group.ToolsCapable
			canUseMedia := group.VisionCapable == nil || *group.VisionCapable
			return checkBindingCapabilities(binding.Binding.Capabilities, canUseTools, canUseMedia)
		}
		return fmt.Errorf("model group does not exist")
	}
	if binding.Kind == "source" {
		sources, err := s.store.ListSources(ctx)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if source.ID == binding.SourceID {
				return nil
			}
		}
		return fmt.Errorf("model source does not exist")
	}
	models, err := s.store.ListModelsFiltered(ctx, storage.ModelListFilter{ShouldIncludeDisabledSources: true})
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.SourceID != binding.SourceID || model.ID != binding.ModelID {
			continue
		}
		return checkBindingCapabilities(binding.Binding.Capabilities, model.ToolsCapable, model.VisionCapable)
	}
	return fmt.Errorf("model binding target does not exist")
}

func checkBindingCapabilities(capabilities protocol.CapabilitySet, canUseTools, canUseMedia bool) error {
	if !canUseTools && hasToolCapability(capabilities) {
		return fmt.Errorf("binding declares tools disabled by the model or group")
	}
	if !canUseMedia && hasMediaCapability(capabilities) {
		return fmt.Errorf("binding declares media disabled by the model or group")
	}
	return nil
}

func selectProtocolBinding(bindings []storage.ProtocolBinding, model config.ModelRef) (storage.ProtocolBinding, bool) {
	var source *storage.ProtocolBinding
	for _, entry := range bindings {
		if entry.SourceID != model.SourceID {
			continue
		}
		if entry.Kind == "model" && entry.ModelID == model.ID {
			return entry, true
		}
		if entry.Kind == "source" {
			value := entry
			source = &value
		}
	}
	if source != nil {
		return *source, true
	}
	return storage.ProtocolBinding{}, false
}

func hasToolCapability(capabilities protocol.CapabilitySet) bool {
	return capabilities[protocol.FunctionToolsCapability] || capabilities[protocol.FreeTextToolsCapability] || capabilities[protocol.ServerToolsCapability]
}

func hasMediaCapability(capabilities protocol.CapabilitySet) bool {
	return capabilities[protocol.ImagesCapability] || capabilities[protocol.AudioCapability] || capabilities[protocol.VideoCapability]
}
