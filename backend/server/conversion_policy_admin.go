package server

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func (s *Server) registerConversionPolicyRoutes(group *route.RouterGroup) {
	p := group.Group("/conversion-policies")
	p.GET("", s.adminConversionPolicies)
	p.PUT("/:policyId/draft", s.adminConversionDraft)
	p.GET("/:policyId/revisions", s.adminConversionRevisions)
	p.POST("/:policyId/verify", s.adminConversionVerify)
	p.POST("/:policyId/probe-signature", s.adminConversionSignatureProbe)
	p.POST("/:policyId/activate", s.adminConversionActivate)
	p.POST("/preview", s.adminConversionPreview)
	group.GET("/continuations", func(ctx context.Context, c *app.RequestContext) {
		stats, err := s.store.ContinuationStats(ctx)
		if err != nil {
			respondProtocolError(ctx, c, err)
			return
		}
		respondOK(ctx, c, stats)
	})
	group.DELETE("/continuations", func(ctx context.Context, c *app.RequestContext) {
		if err := s.store.ClearContinuations(ctx, c.Query("session")); err != nil {
			respondProtocolError(ctx, c, err)
			return
		}
		respondOK(ctx, c, utils.H{"deleted": true})
	})
}

func (s *Server) adminConversionPolicies(ctx context.Context, c *app.RequestContext) {
	items, err := s.store.ListConversionPolicies(ctx, false)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"items": items})
}
func (s *Server) adminConversionDraft(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Policy       protocol.ConversionPolicy `json:"policy"`
		ExpectedHash string                    `json:"expectedHash"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if input.Policy.ID != c.Param("policyId") {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", "policy id differs from route")
		return
	}
	record, err := s.store.SaveConversionDraft(ctx, input.Policy, input.ExpectedHash)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, record)
}
func (s *Server) adminConversionRevisions(ctx context.Context, c *app.RequestContext) {
	items, err := s.store.ConversionRevisions(ctx, c.Param("policyId"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"items": items})
}

func (s *Server) adminConversionVerify(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Hash string `json:"hash"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	drafts, err := s.store.ListConversionPolicies(ctx, false)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	for _, record := range drafts {
		if record.ID != c.Param("policyId") {
			continue
		}
		if input.Hash != record.Hash {
			respondProtocolError(ctx, c, protocol.ErrRevisionConflict)
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
				policy, e := protocol.ResolveConversion(protocol.DefaultConversionPolicy(ingress, upstream), record.Policy)
				if e != nil {
					respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", e.Error())
					return
				}
				record.Reports = append(record.Reports, protocol.VerifyBindingProfiles(ctx, ingress, upstream, upstream.Definition().Capabilities, policy)...)
			}
		}
		record.CompilerVersion = protocol.CompilerVersion
		if err := s.store.SaveConversionRevision(ctx, record); err != nil {
			respondProtocolError(ctx, c, err)
			return
		}
		respondOK(ctx, c, record)
		return
	}
	respondFail(ctx, c, http.StatusNotFound, "not_found", "conversion draft not found")
}

func (s *Server) adminConversionActivate(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Hash           string                   `json:"hash"`
		ExpectedActive string                   `json:"expectedActive"`
		Selector       protocol.ConversionMatch `json:"selector"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	// Selectors choose a protocol pair. Per-model rules belong to the rule's
	// context condition or explicit model binding override.
	if input.Selector.Model != "" || input.Selector.Operation != "" || input.Selector.Transport != "" || input.Selector.NodeKind != "" || input.Selector.Path != "" || input.Selector.Present != nil {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", "policy selector only accepts protocol identity fields")
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	revisions, err := s.store.ConversionRevisions(ctx, c.Param("policyId"))
	if err != nil {
		respondProtocolError(ctx, c, err)
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
		respondFail(ctx, c, http.StatusNotFound, "not_found", "verified policy revision not found")
		return
	}
	if selected.CompilerVersion != protocol.CompilerVersion {
		respondFail(ctx, c, http.StatusBadRequest, "verification_required", "policy verification is stale")
		return
	}
	selected.Selector = input.Selector
	selected.ActiveHash = selected.Hash
	policies, before, generation, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	next := []storage.ConversionPolicyRecord{}
	for _, p := range policies {
		if p.ID == selected.ID {
			if p.Hash == selected.Hash {
				continue
			}
			// Activation changes the default selection, not the immutable revision
			// directory used by bindings pinned to an older revision.
			p.ActiveHash = ""
		}
		next = append(next, p)
	}
	next = append(next, *selected)
	view := service.View()
	// Check every active pair, even if it currently has no bound model.
	for _, source := range view.IDs() {
		a, _ := view.Pin(source)
		for _, target := range view.IDs() {
			b, _ := view.Pin(target)
			if _, err := resolveGatewayConversion(next, nil, a, b, config.ModelRef{}, "", protocol.HTTPJSON); err != nil {
				respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
				return
			}
		}
	}
	after, err := s.verifyConversionBindings(ctx, view, next, before)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	if err := s.store.ActivateConversion(ctx, selected.ID, selected.Hash, input.ExpectedActive, input.Selector, before, after, generation); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	s.invalidateRouteCache()
	respondOK(ctx, c, utils.H{"activeHash": selected.Hash, "bindings": len(after)})
}

func (s *Server) adminConversionPreview(ctx context.Context, c *app.RequestContext) {
	var budgetOK bool
	ctx, budgetOK = s.pinProtocolRequest(ctx, c)
	if !budgetOK {
		return
	}
	var input struct {
		Policy    protocol.ConversionPolicy  `json:"policy"`
		Phase     protocol.ConversionPhase   `json:"phase"`
		Sequence  bool                       `json:"sequence"`
		Direction protocol.Direction         `json:"direction,omitempty"`
		Context   protocol.ConversionContext `json:"context"`
		Input     protocol.Value             `json:"input"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	policy, route, err := service.ResolvePreviewConversion(input.Context, input.Phase, &input.Policy)
	if err == nil {
		route, err = policy.PreviewRecovery(input.Phase, input.Input, route)
	}
	route.ResponseWire = input.Direction == protocol.EncodeResponse || input.Direction == protocol.EncodeEvent
	input.Context = route
	if err != nil {
		respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
		return
	}
	sink := &protocol.DiagnosticSink{}
	var output any
	switch input.Phase {
	case protocol.ConversionSourceEvent:
		output, err = service.PreviewSourceEvents(ctx, policy, input.Context, input.Input, input.Sequence, sink)
	case protocol.ConversionRequest:
		var request protocol.Request
		if err = input.Input.Decode(&request); err == nil {
			output, err = policy.Request(ctx, &request, input.Context, sink)
		}
	case protocol.ConversionResponse:
		var response protocol.Response
		if err = input.Input.Decode(&response); err == nil {
			output, err = policy.Response(ctx, &response, input.Context, sink)
		}
	case protocol.ConversionEvent:
		var event protocol.Event
		if err = input.Input.Decode(&event); err == nil {
			output, err = policy.Event(ctx, event, input.Context, sink)
		}
	default:
		output, err = policy.ApplyValue(ctx, input.Phase, input.Input, input.Context, sink)
	}
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"output": output, "issues": sink.Issues(), "effective": policy, "persistentWrites": false})
}
