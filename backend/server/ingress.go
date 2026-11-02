package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
)

// Public aliases select an explicit protocol ID. Field meanings and error
// representations remain in that revision's Spec and Mapping.
type ingressHandler uint8

const (
	ingressGeneration ingressHandler = iota
	ingressTokenCount
	ingressModelList
	ingressGeminiModelList
)

var publicIngressEndpoints = []struct {
	method, path, protocolID string
	handler                  ingressHandler
}{
	{http.MethodPost, "/v1/chat/completions", protocol.PresetChatCompletionsID, ingressGeneration},
	{http.MethodPost, "/v1/responses", protocol.PresetResponsesID, ingressGeneration},
	{http.MethodPost, "/v1/messages", protocol.PresetAnthropicID, ingressGeneration},
	{http.MethodPost, "/v1/messages/count_tokens", protocol.PresetAnthropicID, ingressTokenCount},
	{http.MethodGet, "/v1/models", protocol.PresetChatCompletionsID, ingressModelList},
	{http.MethodGet, "/v1beta/models", protocol.PresetGeminiID, ingressGeminiModelList},
	{http.MethodPost, "/v1beta/models/*action", protocol.PresetGeminiID, ingressGeneration},
}

const ingressSelectionKey = "elysiaProtocolIngress"

type ingressSelection struct {
	service  *protocol.Service
	view     protocol.RegistryView
	compiled *protocol.Compiled
	path     string
	err      error
}

// pinIngress reads only registered endpoint metadata and the active registry.
// It runs before authentication or body access. Every later entry reuses the
// same snapshot, including a failed selection, across concurrent activation.
func (s *Server) pinIngress(c *app.RequestContext) *ingressSelection {
	if value, exists := c.Get(ingressSelectionKey); exists {
		return value.(*ingressSelection)
	}
	selection := &ingressSelection{path: string(c.Path())}
	c.Set(ingressSelectionKey, selection)
	var id string
	if suffix, found := strings.CutPrefix(selection.path, "/gateway/"); found {
		var path string
		id, path, _ = strings.Cut(suffix, "/")
		selection.path = "/" + path
	} else {
		for _, endpoint := range publicIngressEndpoints {
			prefix, _, wildcard := strings.Cut(endpoint.path, "*")
			if endpoint.method == string(c.Method()) && (endpoint.path == selection.path || wildcard && strings.HasPrefix(selection.path, prefix) && len(selection.path) > len(prefix)) {
				id = endpoint.protocolID
				break
			}
		}
	}
	if id == "" {
		selection.err = &gatewayFailure{http.StatusNotFound, fmt.Errorf("endpoint has no registered ingress protocol")}
		return selection
	}
	if err := s.protocolRuntimeError(); err != nil {
		selection.err = &gatewayFailure{http.StatusServiceUnavailable, err}
		return selection
	}
	service, err := s.protocolService()
	if err != nil {
		selection.err = &gatewayFailure{http.StatusServiceUnavailable, err}
		return selection
	}
	selection.service, selection.view = service, service.View()
	compiled, exists := selection.view.Pin(id)
	if !exists {
		selection.err = &gatewayFailure{http.StatusServiceUnavailable, fmt.Errorf("endpoint requires a verified active protocol revision")}
		return selection
	}
	selection.compiled = compiled
	return selection
}

func (s *Server) requireIngress(c *app.RequestContext) (*ingressSelection, bool) {
	selection := s.pinIngress(c)
	if selection.err == nil {
		return selection, true
	}
	failure := selection.err.(*gatewayFailure)
	writeGatewayError(c, failure.status, failure.cause.Error())
	return selection, false
}
