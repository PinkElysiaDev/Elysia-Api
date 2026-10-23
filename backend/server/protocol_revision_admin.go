package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

func (s *Server) protocolService() (*protocol.Service, error) {
	s.protocolServiceOnce.Do(func() {
		compiler, err := relay.NewProtocolCompiler(protocol.DefaultLimits())
		if err != nil {
			s.protocolServiceErr = err
			return
		}
		if s.store == nil {
			s.protocolServiceErr = fmt.Errorf("protocol revision storage is unavailable")
			return
		}
		service, err := protocol.NewService(compiler, s.store)
		if err != nil {
			s.protocolServiceErr = err
			return
		}
		s.protocolServiceInst = service
		if err := service.Reload(context.Background()); err != nil {
			// The management service remains available to repair invalid/stale
			// revisions; no invalid definition is published to the registry.
			if isMissingProtocolEvidence(err) {
				s.logEvent("info").Str("compiler", protocol.CompilerVersion).Err(err).Msg("protocol evidence refresh required")
			} else {
				s.logEvent("error").Str("compiler", protocol.CompilerVersion).Err(err).Msg("protocol registry requires repair")
			}
		}
	})
	return s.protocolServiceInst, s.protocolServiceErr
}

func isMissingProtocolEvidence(err error) bool {
	var failure *protocol.ConversionError
	return errors.Is(err, protocol.ErrNotFound) && errors.As(err, &failure) &&
		len(failure.Issues) == 1 && failure.Issues[0].Code == protocol.VerificationRequired && failure.Issues[0].Path == "/verification"
}

func (s *Server) requireProtocolService(ctx context.Context, c *app.RequestContext) (*protocol.Service, bool) {
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(ctx, c, err)
		return nil, false
	}
	return service, true
}

func (s *Server) setupProtocolRevisionRoutes(admin *route.RouterGroup) {
	group := admin.Group("/protocols")
	s.registerConversionPolicyRoutes(group)
	group.GET("", s.adminProtocolDrafts)
	group.GET("/enabled", s.adminEnabledProtocols)
	group.GET("/schema", s.adminProtocolSchemaV2)
	group.GET("/history", s.adminProtocolHistory)
	group.GET("/history/:archiveId", s.adminProtocolHistoryDetail)
	group.POST("/history/:archiveId/restore", s.adminRestoreProtocolHistory)
	group.DELETE("/history/:archiveId", s.adminDeleteProtocolHistory)
	group.GET("/:id/references", s.adminProtocolReferences)
	group.POST("/:id/archive", s.adminArchiveProtocol)
	group.POST("/validate", s.adminProtocolValidate)
	group.POST("/preview", s.adminProtocolPreviewV2)
	group.POST("/test", s.adminProtocolProbe)
	group.POST("/reload", s.adminProtocolReload)
	group.POST("/combinations", s.adminProtocolCombination)
	group.GET("/bindings", s.adminProtocolBindings)
	group.PUT("/bindings", s.adminSaveProtocolBinding)
	group.GET("/:id/draft", s.adminProtocolDraft)
	group.PUT("/:id/draft", s.adminProtocolSaveDraft)
	group.POST("/:id/verify", s.adminProtocolVerifyDraft)
	group.GET("/:id/revisions", s.adminProtocolRevisions)
	group.GET("/:id/revisions/:hash", s.adminProtocolRevision)
	group.GET("/:id/revisions/:hash/upstream-reports", s.adminProtocolUpstreamReports)
	group.POST("/:id/revisions/:hash/verify", s.adminProtocolVerifyRevision)
	group.GET("/:id/diff", s.adminProtocolRevisionDiff)
	group.POST("/:id/activate", s.adminProtocolActivate)
	group.POST("/:id/rollback", s.adminProtocolActivate)
}

func readProtocolAdminBody(ctx context.Context, c *app.RequestContext) (result []byte, err error) {
	defer func() { err = protocolManagementInputError(err) }()
	limit := protocolAdminMaxBodyBytes
	if protocolPreviewPath(string(c.Path())) {
		limit = protocol.PreviewEnvelopeLimit(ctx)
	}
	body, err := io.ReadAll(io.LimitReader(requestBody(c), int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("protocol request exceeds body limit")
	}
	return body, nil
}

func decodeProtocolAdminBody(ctx context.Context, c *app.RequestContext, target any) (err error) {
	defer func() { err = protocolManagementInputError(err) }()
	body, err := readProtocolAdminBody(ctx, c)
	if err != nil {
		return err
	}
	decoder := sonic.ConfigStd.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}

func protocolManagementInputError(err error) error {
	if err == nil {
		return nil
	}
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		return err
	}
	return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.InvalidInput, Severity: protocol.SeverityError, Stage: "management", Path: "/", Reason: err.Error(), Suggestion: "Supply a valid request using the current protocol management schema."}})
}

func respondProtocolError(ctx context.Context, c *app.RequestContext, err error) {
	status, code := http.StatusInternalServerError, "protocol_service_error"
	if errors.Is(err, protocol.ErrRevisionConflict) {
		status, code = http.StatusConflict, "revision_conflict"
	}
	if errors.Is(err, protocol.ErrNotFound) {
		status, code = http.StatusNotFound, "not_found"
	}
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		status, code = http.StatusBadRequest, "invalid_protocol"
		c.JSON(status, utils.H{"ok": false, "error": utils.H{"code": code, "message": err.Error(), "issues": conversion.Issues}})
		return
	}
	respondFail(ctx, c, status, code, err.Error())
}

func (s *Server) adminProtocolDrafts(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	drafts, err := service.ListDrafts(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	active, err := service.Activations(ctx)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	loaded := make(map[string]string, len(active))
	for _, activation := range active {
		if pinned, exists := service.Pin(activation.ProtocolID); exists {
			loaded[activation.ProtocolID] = pinned.Hash()
		}
	}
	failures := service.View().Failures()
	for _, id := range requiredPresetIDs {
		if loaded[id] == "" {
			failures[id] = "预置未成功加载，请重新加载协议并检查启动日志"
		}
	}
	result := utils.H{"drafts": drafts, "active": active, "loaded": loaded, "presets": requiredPresetIDs, "runtimeFailures": failures, "runtimeReady": s.protocolRuntimeReady.Load()}
	if failure := s.protocolStartupFailure.Load(); failure != nil {
		result["startupFailure"] = failure
	}
	if reason := s.protocolRuntimeFailure.Load(); reason != nil {
		result["runtimeError"] = *reason
	}
	respondOK(ctx, c, result)
}

func (s *Server) adminProtocolSchemaV2(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if ok {
		respondOK(ctx, c, service.Schema())
	}
}

func (s *Server) adminProtocolValidate(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	body, err := readProtocolAdminBody(ctx, c)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	compiled, issues := service.Validate(body)
	result := utils.H{"valid": compiled != nil, "issues": issues}
	if compiled != nil {
		result["hash"], result["identity"] = compiled.Hash(), compiled.Identity()
	}
	respondOK(ctx, c, result)
}

func (s *Server) adminProtocolDraft(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	draft, err := service.ReadDraft(ctx, c.Param("id"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	c.Header("ETag", `"`+draft.Hash+`"`)
	respondOK(ctx, c, draft)
}

func (s *Server) adminProtocolSaveDraft(ctx context.Context, c *app.RequestContext) {
	body, err := readProtocolAdminBody(ctx, c)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	s.saveProtocolDraft(ctx, c, c.Param("id"), body)
}

func (s *Server) saveProtocolDraft(ctx context.Context, c *app.RequestContext, id string, body []byte) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	expected := strings.Trim(string(c.GetHeader("If-Match")), `"`)
	draft, issues, err := service.SaveDraft(ctx, id, body, expected)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	c.Header("ETag", `"`+draft.Hash+`"`)
	respondOK(ctx, c, utils.H{"draft": draft, "issues": issues, "activated": false})
}

func (s *Server) adminProtocolVerifyDraft(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	var input struct {
		DraftHash string `json:"draftHash"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	revision, report, err := service.VerifyDraft(ctx, c.Param("id"), input.DraftHash)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"revision": revision, "report": report})
}

func (s *Server) adminProtocolRevisions(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	revisions, err := service.Revisions(ctx, c.Param("id"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"items": revisions})
}

func (s *Server) adminProtocolRevision(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	revision, err := service.ReadRevision(ctx, c.Param("id"), c.Param("hash"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	report, err := service.ReadReport(ctx, c.Param("id"), c.Param("hash"))
	if err != nil && !errors.Is(err, protocol.ErrNotFound) {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"revision": revision, "report": report})
}

func (s *Server) adminProtocolVerifyRevision(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	report, err := service.VerifyRevision(ctx, c.Param("id"), c.Param("hash"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, report)
}

func (s *Server) adminProtocolActivate(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	var input struct {
		RevisionHash   string `json:"revisionHash"`
		ExpectedActive string `json:"expectedActive"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	activation, err := service.Activate(ctx, c.Param("id"), input.RevisionHash, input.ExpectedActive)
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, activation)
}

func (s *Server) adminProtocolReload(ctx context.Context, c *app.RequestContext) {
	if err := s.initializeProtocolRuntime(ctx); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"reloaded": true})
}

func (s *Server) adminProtocolRevisionDiff(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	changes, err := service.Diff(ctx, c.Param("id"), c.Query("from"), c.Query("to"))
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, utils.H{"changes": changes})
}

func (s *Server) adminProtocolPreviewV2(ctx context.Context, c *app.RequestContext) {
	var budgetOK bool
	ctx, budgetOK = s.pinProtocolRequest(ctx, c)
	if !budgetOK {
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	var input protocol.PreviewInput
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	respondOK(ctx, c, service.PreviewWorkflow(ctx, input))
}

func (s *Server) adminProtocolCombination(ctx context.Context, c *app.RequestContext) {
	var budgetOK bool
	ctx, budgetOK = s.pinProtocolRequest(ctx, c)
	if !budgetOK {
		return
	}
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	var input struct {
		Ingress          json.RawMessage            `json:"ingress"`
		ConversionPolicy *protocol.ConversionPolicy `json:"conversionPolicy,omitempty"`
		Capabilities     protocol.CapabilitySet     `json:"capabilities,omitempty"`
		Upstream         json.RawMessage            `json:"upstream"`
	}
	if err := decodeProtocolAdminBody(ctx, c, &input); err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	ingress, first := service.Validate(input.Ingress)
	upstream, second := service.Validate(input.Upstream)
	if issues := append(first, second...); protocol.IssuesError(issues) != nil {
		respondProtocolError(ctx, c, protocol.IssuesError(issues))
		return
	}
	if input.ConversionPolicy != nil {
		conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(ingress, upstream), *input.ConversionPolicy)
		if err != nil {
			respondFail(ctx, c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
			return
		}
		respondOK(ctx, c, protocol.VerifyBindingCombination(ctx, ingress, upstream, input.Capabilities, conversion))
		return
	}
	respondOK(ctx, c, protocol.VerifyCombination(ctx, ingress, upstream))
}
