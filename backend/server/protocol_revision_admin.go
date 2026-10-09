package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
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
			log.Printf("protocol registry requires repair: %v", err)
		}
	})
	return s.protocolServiceInst, s.protocolServiceErr
}

func (s *Server) requireProtocolService(c *gin.Context) (*protocol.Service, bool) {
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(c, err)
		return nil, false
	}
	return service, true
}

func (s *Server) setupProtocolRevisionRoutes(admin *gin.RouterGroup) {
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
	group.GET("/migration", s.adminProtocolUpgradeStatus)
	group.POST("/migration/preview", s.adminProtocolUpgradePreview)
	group.POST("/migration/apply", s.adminProtocolUpgradeApply)
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

func readProtocolAdminBody(c *gin.Context) (result []byte, err error) {
	defer func() { err = protocolManagementInputError(err) }()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, protocolAdminMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > protocolAdminMaxBodyBytes {
		return nil, fmt.Errorf("protocol request exceeds body limit")
	}
	return body, nil
}

func decodeProtocolAdminBody(c *gin.Context, target any) (err error) {
	defer func() { err = protocolManagementInputError(err) }()
	body, err := readProtocolAdminBody(c)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
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

func respondProtocolError(c *gin.Context, err error) {
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
		c.JSON(status, gin.H{"ok": false, "error": gin.H{"code": code, "message": err.Error(), "issues": conversion.Issues}})
		return
	}
	respondFail(c, status, code, err.Error())
}

func (s *Server) adminProtocolDrafts(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	drafts, err := service.ListDrafts(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	active, err := service.Activations(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	loaded := make(map[string]string, len(active))
	for _, activation := range active {
		if pinned, exists := service.Pin(activation.ProtocolID); exists {
			loaded[activation.ProtocolID] = pinned.Hash()
		}
	}
	presets := make([]string, 0)
	for _, activation := range active {
		if protocol.IsPresetProtocolID(activation.ProtocolID) {
			presets = append(presets, activation.ProtocolID)
		}
	}
	respondOK(c, gin.H{"drafts": drafts, "active": active, "loaded": loaded, "presets": presets, "runtimeFailures": service.View().Failures()})
}

func (s *Server) adminProtocolSchemaV2(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if ok {
		respondOK(c, service.Schema())
	}
}

func (s *Server) adminProtocolValidate(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	body, err := readProtocolAdminBody(c)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	compiled, issues := service.Validate(body)
	result := gin.H{"valid": compiled != nil, "issues": issues}
	if compiled != nil {
		result["hash"], result["identity"] = compiled.Hash(), compiled.Identity()
	}
	respondOK(c, result)
}

func (s *Server) adminProtocolDraft(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	draft, err := service.ReadDraft(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	c.Header("ETag", `"`+draft.Hash+`"`)
	respondOK(c, draft)
}

func (s *Server) adminProtocolSaveDraft(c *gin.Context) {
	body, err := readProtocolAdminBody(c)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	s.saveProtocolDraft(c, c.Param("id"), body)
}

func (s *Server) saveProtocolDraft(c *gin.Context, id string, body []byte) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	expected := strings.Trim(c.GetHeader("If-Match"), `"`)
	draft, issues, err := service.SaveDraft(c.Request.Context(), id, body, expected)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	c.Header("ETag", `"`+draft.Hash+`"`)
	respondOK(c, gin.H{"draft": draft, "issues": issues, "activated": false})
}

func (s *Server) adminProtocolVerifyDraft(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	var input struct {
		DraftHash string `json:"draftHash"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	revision, report, err := service.VerifyDraft(c.Request.Context(), c.Param("id"), input.DraftHash)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"revision": revision, "report": report})
}

func (s *Server) adminProtocolRevisions(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	revisions, err := service.Revisions(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"items": revisions})
}

func (s *Server) adminProtocolRevision(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	revision, err := service.ReadRevision(c.Request.Context(), c.Param("id"), c.Param("hash"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	report, err := service.ReadReport(c.Request.Context(), c.Param("id"), c.Param("hash"))
	if err != nil && !errors.Is(err, protocol.ErrNotFound) {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"revision": revision, "report": report})
}

func (s *Server) adminProtocolVerifyRevision(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	report, err := service.VerifyRevision(c.Request.Context(), c.Param("id"), c.Param("hash"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, report)
}

func (s *Server) adminProtocolActivate(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	var input struct {
		RevisionHash   string `json:"revisionHash"`
		ExpectedActive string `json:"expectedActive"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	activation, err := service.Activate(c.Request.Context(), c.Param("id"), input.RevisionHash, input.ExpectedActive)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, activation)
}

func (s *Server) adminProtocolReload(c *gin.Context) {
	if err := s.reloadProtocolRuntime(c.Request.Context()); err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"reloaded": true})
}

func (s *Server) adminProtocolRevisionDiff(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	changes, err := service.Diff(c.Request.Context(), c.Param("id"), c.Query("from"), c.Query("to"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, gin.H{"changes": changes})
}

func (s *Server) adminProtocolPreviewV2(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	var input protocol.PreviewInput
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, service.PreviewWorkflow(c.Request.Context(), input))
}

func (s *Server) adminProtocolCombination(c *gin.Context) {
	service, ok := s.requireProtocolService(c)
	if !ok {
		return
	}
	var input struct {
		Ingress          json.RawMessage            `json:"ingress"`
		ConversionPolicy *protocol.ConversionPolicy `json:"conversionPolicy,omitempty"`
		Capabilities     protocol.CapabilitySet     `json:"capabilities,omitempty"`
		Upstream         json.RawMessage            `json:"upstream"`
	}
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	ingress, first := service.Validate(input.Ingress)
	upstream, second := service.Validate(input.Upstream)
	if issues := append(first, second...); protocol.IssuesError(issues) != nil {
		respondProtocolError(c, protocol.IssuesError(issues))
		return
	}
	if input.ConversionPolicy != nil {
		conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(ingress, upstream), *input.ConversionPolicy)
		if err != nil {
			respondFail(c, http.StatusBadRequest, "invalid_conversion_policy", err.Error())
			return
		}
		respondOK(c, protocol.VerifyBindingCombination(c.Request.Context(), ingress, upstream, input.Capabilities, conversion))
		return
	}
	respondOK(c, protocol.VerifyCombination(c.Request.Context(), ingress, upstream))
}
