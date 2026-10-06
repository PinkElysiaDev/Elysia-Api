package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) adminProtocolBindings(c *gin.Context) {
	bindings, err := s.store.ListProtocolBindings(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, bindings)
}

func (s *Server) adminSaveProtocolBinding(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	var binding storage.ProtocolBinding
	if err := decodeProtocolAdminBody(c, &binding); err != nil {
		respondProtocolError(c, err)
		return
	}
	compiled, _ := service.Pin(binding.Binding.ProtocolID)
	issues := protocol.CheckBinding(binding.Binding, compiled)
	if binding.Kind == "group" {
		issues = protocol.CheckIngressBinding(binding.Binding, compiled)
	}
	if err := protocol.IssuesError(issues); err != nil {
		respondProtocolError(c, err)
		return
	}
	if err := s.checkProtocolBindingTarget(c.Request.Context(), binding); err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_binding", err.Error())
		return
	}
	if binding.Kind != "group" {
		binding.Combinations = verifyGatewayBinding(c.Request.Context(), service.View(), compiled, binding.Binding.Capabilities)
		if !hasPassingGatewayCombination(binding.Combinations) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": gin.H{"code": "incompatible_binding", "message": "no active ingress has verified compatibility with this model contract", "combinations": binding.Combinations}})
			return
		}
	}
	if err := s.store.SaveProtocolBinding(c.Request.Context(), binding); err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, binding)
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
