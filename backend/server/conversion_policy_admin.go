package server

import (
	"net/http"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func (s *Server) registerConversionPolicyRoutes(group *gin.RouterGroup) {
	p := group.Group("/conversion-policies")
	p.GET("", s.adminConversionPolicies)
	p.PUT("/:policyId/draft", s.adminConversionDraft)
	p.GET("/:policyId/revisions", s.adminConversionRevisions)
	p.POST("/:policyId/verify", s.adminConversionVerify)
	p.POST("/:policyId/probe-signature", s.adminConversionSignatureProbe)
	p.POST("/:policyId/activate", s.adminConversionActivate)
	p.POST("/preview", s.adminConversionPreview)
	group.GET("/continuations", func(c *gin.Context) {
		stats, err := s.store.ContinuationStats(c.Request.Context())
		if err != nil {
			respondProtocolError(c, err)
			return
		}
		respondOK(c, stats)
	})
	group.DELETE("/continuations", func(c *gin.Context) {
		if err := s.store.ClearContinuations(c.Request.Context(), c.Query("session")); err != nil {
			respondProtocolError(c, err)
			return
		}
		respondOK(c, gin.H{"deleted": true})
	})
}

func (s *Server) adminConversionPolicies(c *gin.Context) {
	items, err := s.store.ListConversionPolicies(c.Request.Context(), false)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"items": items})
}
func (s *Server) adminConversionDraft(c *gin.Context) {
	var input struct {
		Policy       protocol.ConversionPolicy `json:"policy"`
		ExpectedHash string                    `json:"expectedHash"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	if input.Policy.ID != c.Param("policyId") {
		respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", "policy id differs from route")
		return
	}
	record, err := s.store.SaveConversionDraft(c.Request.Context(), input.Policy, input.ExpectedHash)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, record)
}
func (s *Server) adminConversionRevisions(c *gin.Context) {
	items, err := s.store.ConversionRevisions(c.Request.Context(), c.Param("policyId"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"items": items})
}

func (s *Server) adminConversionVerify(c *gin.Context) {
	var input struct {
		Hash string `json:"hash"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	drafts, err := s.store.ListConversionPolicies(c.Request.Context(), false)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	for _, record := range drafts {
		if record.ID != c.Param("policyId") {
			continue
		}
		if input.Hash != record.Hash {
			respondProtocolError(c, protocol.ErrRevisionConflict)
			return
		}
		view := service.View()
		for _, source := range view.IDs() {
			ingress, _ := view.Pin(source)
			if !ingress.Supports(protocol.DecodeRequest) {
				continue
			}
			for _, target := range view.IDs() {
				upstream, _ := view.Pin(target)
				if !upstream.Supports(protocol.EncodeRequest) {
					continue
				}
				policy, e := protocol.ResolveConversion(protocol.DefaultConversionPolicy(ingress.Identity(), upstream.Identity()), record.Policy)
				if e != nil {
					respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", e.Error())
					return
				}
				record.Reports = append(record.Reports, protocol.VerifyBindingProfiles(c.Request.Context(), ingress, upstream, upstream.Definition().Capabilities, policy)...)
			}
		}
		record.CompilerVersion = protocol.CompilerVersion
		if err := s.store.SaveConversionRevision(c.Request.Context(), record); err != nil {
			respondProtocolError(c, err)
			return
		}
		respondOK(c, record)
		return
	}
	respondFail(c, http.StatusNotFound, "not_found", "conversion draft not found")
}

func (s *Server) adminConversionActivate(c *gin.Context) {
	var input struct {
		Hash           string                   `json:"hash"`
		ExpectedActive string                   `json:"expectedActive"`
		Selector       protocol.ConversionMatch `json:"selector"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	// Selectors choose a protocol pair. Per-model rules belong to the rule's
	// context condition or explicit model binding override.
	if input.Selector.Model != "" || input.Selector.Operation != "" || input.Selector.Transport != "" || input.Selector.NodeKind != "" || input.Selector.Path != "" || input.Selector.Present != nil {
		respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", "policy selector only accepts protocol identity fields")
		return
	}
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	revisions, err := s.store.ConversionRevisions(c.Request.Context(), c.Param("policyId"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	var selected *storage.ConversionPolicyRecord
	for i := range revisions {
		if revisions[i].Hash == input.Hash {
			selected = &revisions[i]
			break
		}
	}
	if selected == nil {
		respondFail(c, http.StatusNotFound, "not_found", "verified policy revision not found")
		return
	}
	if selected.CompilerVersion != protocol.CompilerVersion {
		respondFail(c, http.StatusBadRequest, "verification_required", "policy verification is stale")
		return
	}
	selected.Selector = input.Selector
	selected.ActiveHash = selected.Hash
	policies, before, generation, err := s.store.ConversionSnapshot(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	next := []storage.ConversionPolicyRecord{}
	for _, p := range policies {
		if p.ID != selected.ID {
			next = append(next, p)
		}
	}
	next = append(next, *selected)
	view := service.View()
	// Check every active pair, even if it currently has no bound model.
	for _, source := range view.IDs() {
		a, _ := view.Pin(source)
		for _, target := range view.IDs() {
			b, _ := view.Pin(target)
			if _, err := resolveGatewayConversion(next, nil, a, b, config.ModelRef{}, "", protocol.HTTPJSON); err != nil {
				respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
				return
			}
		}
	}
	after, err := s.verifyConversionBindings(c.Request.Context(), view, next, before)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	if err := s.store.ActivateConversion(c.Request.Context(), selected.ID, selected.Hash, input.ExpectedActive, input.Selector, before, after, generation); err != nil {
		respondProtocolError(c, err)
		return
	}
	s.invalidateRouteCache()
	respondOK(c, gin.H{"activeHash": selected.Hash, "bindings": len(after)})
}

func (s *Server) adminConversionPreview(c *gin.Context) {
	var input struct {
		Policy  protocol.ConversionPolicy  `json:"policy"`
		Phase   protocol.ConversionPhase   `json:"phase"`
		Context protocol.ConversionContext `json:"context"`
		Input   protocol.Value             `json:"input"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	policy, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(input.Context.Source, input.Context.Target), input.Policy)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
		return
	}
	sink := &protocol.DiagnosticSink{}
	var output any
	switch input.Phase {
	case protocol.ConversionRequest:
		var request protocol.Request
		if err = input.Input.Decode(&request); err == nil {
			output, err = policy.Request(c.Request.Context(), &request, input.Context, sink)
		}
	case protocol.ConversionResponse:
		var response protocol.Response
		if err = input.Input.Decode(&response); err == nil {
			output, err = policy.Response(c.Request.Context(), &response, input.Context, sink)
		}
	case protocol.ConversionEvent:
		var event protocol.Event
		if err = input.Input.Decode(&event); err == nil {
			output, err = policy.Event(c.Request.Context(), event, input.Context, sink)
		}
	default:
		output, err = policy.ApplyValue(c.Request.Context(), input.Phase, input.Input, input.Context, sink)
	}
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"output": output, "issues": sink.Issues(), "effective": policy, "persistentWrites": false})
}
