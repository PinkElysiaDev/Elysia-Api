package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const maxModelDiscoveryPages = 100

func (s *Server) sourceProtocol(source storage.ModelSource) (*protocol.Compiled, error) {
	if source.Platform == "" {
		return nil, gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/binding", "模型源未绑定协议，请先选择协议")
	}
	id, err := protocolIDForPlatform(source.Platform)
	if err != nil {
		return nil, err
	}
	service, err := s.protocolService()
	if err != nil {
		return nil, err
	}
	compiled, isActive := service.Pin(id)
	if !isActive {
		return nil, gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/platform", "model source requires an active verified protocol: "+id)
	}
	return compiled, nil
}

func selectModelDiscovery(compiled *protocol.Compiled) (string, protocol.Operation, error) {
	var name string
	var selected protocol.Operation
	for key, operation := range compiled.Operations() {
		if operation.Kind != "models" {
			continue
		}
		if name != "" {
			return "", selected, fmt.Errorf("model discovery requires one unambiguous models operation")
		}
		name, selected = key, operation
	}
	if name == "" {
		return "", selected, fmt.Errorf("protocol %q does not define model discovery; declare a models operation or use manual models", compiled.Identity().DefinitionID)
	}
	return name, selected, nil
}

func (s *Server) fetchModelsFromSource(ctx context.Context, source storage.ModelSource, apiKey string) ([]storage.Model, error) {
	compiled, err := s.sourceProtocol(source)
	if err != nil {
		return nil, err
	}
	entries, err := s.discoverProtocolModels(ctx, compiled, sourceFetchBase(source), apiKey)
	if err != nil {
		return nil, err
	}
	models := make([]storage.Model, 0, len(entries))
	for _, entry := range entries {
		model := inferredModel(source, entry.ID, entry.ID)
		model.MaxTokens = entry.MaxTokens
		s.enrichModelFromCatalog(&model)
		models = append(models, model)
	}
	return models, nil
}

func (s *Server) discoverProtocolModels(ctx context.Context, compiled *protocol.Compiled, baseURL, apiKey string) ([]protocol.DiscoveredModel, error) {
	if err := s.validateOutbound(baseURL); err != nil {
		return nil, err
	}
	name, operation, err := selectModelDiscovery(compiled)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, modelFetchTimeout)
	defer cancel()
	models := []protocol.DiscoveredModel{}
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	limits := compiled.ResourceLimits()
	for range maxModelDiscoveryPages {
		response, err := s.protocolTransport.SendProtocolRequest(ctx, baseURL, apiKey, operation, nil, nil)
		if err != nil {
			return nil, err
		}
		body, err := protocol.ReadBoundedBody(response.Body, limits.BufferBytes)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			// Provider bodies may echo credentials; keep them out of refresh logs.
			return nil, fmt.Errorf("model discovery failed: HTTP %d", response.StatusCode)
		}
		value, err := protocol.ParseValue(body)
		if err != nil {
			return nil, err
		}
		page, err := compiled.DecodeModelPage(ctx, name, value)
		if err != nil {
			return nil, err
		}
		if len(models)+len(page.Models) > limits.StateItems {
			return nil, fmt.Errorf("model discovery exceeded catalog item limit")
		}
		for _, entry := range page.Models {
			if seenIDs[entry.ID] {
				return nil, fmt.Errorf("model discovery returned a duplicate identity across pages")
			}
			seenIDs[entry.ID] = true
			models = append(models, entry)
		}
		if page.Next == "" {
			return models, nil
		}
		if seenCursors[page.Next] {
			return nil, fmt.Errorf("model discovery repeated a pagination cursor")
		}
		seenCursors[page.Next] = true
		if operation.Query == nil {
			operation.Query = map[string]string{}
		}
		operation.Query[operation.Models.CursorParameter] = page.Next
	}
	return nil, fmt.Errorf("model discovery exceeded pagination limit")
}
