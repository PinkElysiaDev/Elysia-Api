package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

type protocolProbeInput struct {
	Definition protocol.Value   `json:"definition"`
	Request    protocol.Request `json:"request"`
	Operation  string           `json:"operation"`
	BaseURL    string           `json:"baseUrl"`
	APIKey     string           `json:"apiKey"`
}

type protocolProbeResult struct {
	Report   protocol.VerificationReport `json:"report"`
	Response *protocol.Response          `json:"response,omitempty"`
}

func (s *Server) probeProtocol(ctx context.Context, input protocolProbeInput) (*protocolProbeResult, error) {
	service, err := s.protocolService()
	if err != nil {
		return nil, err
	}
	compiled, issues := service.Validate(input.Definition.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		return nil, err
	}
	offline := protocol.Verify(ctx, compiled)
	if !offline.Passed {
		return nil, protocol.IssuesError(offline.Issues)
	}
	operation, exists := compiled.Operations()[input.Operation]
	if !exists || operation.Kind != "generate" || operation.Transport == protocol.WebSocket {
		return nil, fmt.Errorf("select a declared HTTP generation operation; session/task/model discovery probes require their own workflow")
	}
	var model string
	if err := input.Request.Model.Decode(&model); err != nil || model == "" {
		return nil, fmt.Errorf("probe requires an explicit semantic request and model")
	}
	endpoint := sha256.Sum256([]byte(input.BaseURL))
	ref := config.ModelRef{Name: model, BaseURL: input.BaseURL, APIKey: input.APIKey, SourceID: hex.EncodeToString(endpoint[:])}
	scope := modelProtocolScope(ref)
	binding := protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Capabilities(protocol.EncodeRequest), Transports: []protocol.Transport{operation.Transport}, Operation: input.Operation}
	candidate := gatewayCandidate{model: ref, compiled: compiled, binding: binding, operation: operation, scope: scope}
	response, runErr := s.collectProtocolGeneration(ctx, candidate, &input.Request, nil, nil)
	report := protocol.VerificationReport{DefinitionHash: compiled.Hash(), CompilerVersion: protocol.CompilerVersion, SamplesHash: compiled.SamplesHash(), Kind: protocol.UpstreamVerification, VerifiedAt: time.Now().UTC(), Passed: runErr == nil, Target: &scope, Checks: []protocol.VerificationCheck{{SampleID: input.Operation, Direction: protocol.DecodeResponse, Passed: runErr == nil, Reason: "Observed request/response contract for this target only; does not prove all capabilities or cache hits."}}}
	if runErr != nil {
		var conversion *protocol.ConversionError
		if errors.As(runErr, &conversion) {
			report.Issues = conversion.Issues
		} else {
			report.Issues = []protocol.ConversionIssue{{Code: protocol.UpstreamContractViolation, Severity: protocol.SeverityError, Protocol: compiled.Identity(), Stage: "upstream_probe", Path: "/", Reason: "Upstream request failed; verify the target, credentials and declared contract."}}
		}
	}
	// Store metadata only; credentials and captured model output stay out of reports.
	revision := protocol.Revision{ProtocolID: compiled.Identity().DefinitionID, Hash: compiled.Hash(), Definition: input.Definition, CreatedAt: report.VerifiedAt}
	if err := s.store.SaveProtocolRevision(ctx, revision); err != nil {
		return nil, err
	}
	if err := s.store.SaveProtocolReport(ctx, revision.ProtocolID, revision.Hash, report); err != nil {
		return nil, err
	}
	return &protocolProbeResult{Report: report, Response: response}, nil
}

func (s *Server) adminProtocolProbe(c *gin.Context) {
	var input protocolProbeInput
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	result, err := s.probeProtocol(c.Request.Context(), input)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, result)
}

func (s *Server) adminProtocolUpstreamReports(c *gin.Context) {
	if _, ok := s.requireProtocolService(c); !ok {
		return
	}
	reports, err := s.store.ListProtocolUpstreamReports(c.Request.Context(), c.Param("id"), c.Param("hash"))
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, reports)
}
