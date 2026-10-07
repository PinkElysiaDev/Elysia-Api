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
	Models   []protocol.DiscoveredModel  `json:"models,omitempty"`
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
	if !exists || (operation.Kind != "generate" && operation.Kind != "models") || operation.Transport == protocol.WebSocket {
		return nil, fmt.Errorf("select a declared HTTP generation or model discovery operation; sessions and tasks require their own workflow")
	}
	var model string
	if operation.Kind == "generate" {
		if err := input.Request.Model.Decode(&model); err != nil || model == "" {
			return nil, fmt.Errorf("generation probe requires an explicit semantic request and model")
		}
	}
	endpoint := sha256.Sum256([]byte(input.BaseURL))
	ref := config.ModelRef{Name: model, BaseURL: input.BaseURL, APIKey: input.APIKey, SourceID: hex.EncodeToString(endpoint[:])}
	scope := modelProtocolScope(ref)
	result := &protocolProbeResult{}
	var runErr error
	if operation.Kind == "models" {
		result.Models, runErr = s.discoverProtocolModels(ctx, compiled, input.BaseURL, input.APIKey)
	} else {
		binding := protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Capabilities(protocol.EncodeRequest), Transports: []protocol.Transport{operation.Transport}, Operation: input.Operation}
		candidate := gatewayCandidate{model: ref, compiled: compiled, binding: binding, operation: operation, scope: scope}
		result.Response, runErr = s.collectProtocolGeneration(ctx, candidate, &input.Request, nil, nil)
	}
	report := protocol.VerificationReport{DefinitionHash: compiled.Hash(), CompilerVersion: protocol.CompilerVersion, SamplesHash: compiled.SamplesHash(), Kind: protocol.UpstreamVerification, VerifiedAt: time.Now().UTC(), Passed: runErr == nil, Target: &scope, Checks: []protocol.VerificationCheck{{SampleID: input.Operation, Direction: protocol.DecodeResponse, Passed: runErr == nil, Reason: "Observed " + operation.Kind + " contract for this target only; does not prove other operations, all capabilities or cache hits."}}}
	if runErr != nil {
		var conversion *protocol.ConversionError
		if errors.As(runErr, &conversion) {
			report.Issues = conversion.Issues
		} else {
			report.Issues = []protocol.ConversionIssue{{Code: protocol.UpstreamContractViolation, Severity: protocol.SeverityError, Protocol: compiled.Identity(), Stage: "upstream_probe", Path: "/", Reason: "Upstream request failed; verify the target, credentials and declared contract."}}
		}
	}
	// Store metadata only; credentials and captured model output stay out of reports.
	// 预置只读：探测证据只落在当前激活（shipped）修订上。旧政策遗留的预置
	// draft 不能借 test 探测把已废弃的编辑重新写回修订历史；探针结果本身
	// 仍照常返回给调用方。
	if id := compiled.Identity().DefinitionID; protocol.IsPresetProtocolID(id) {
		activated, err := s.activeRevisionHash(ctx, id)
		if err != nil {
			return nil, err
		}
		if activated != compiled.Hash() {
			result.Report = report
			return result, nil
		}
	}
	revision := protocol.Revision{ProtocolID: compiled.Identity().DefinitionID, Hash: compiled.Hash(), Definition: input.Definition, CreatedAt: report.VerifiedAt}
	if err := s.store.SaveProtocolRevision(ctx, revision); err != nil {
		return nil, err
	}
	if err := s.store.SaveProtocolReport(ctx, revision.ProtocolID, revision.Hash, report); err != nil {
		return nil, err
	}
	result.Report = report
	return result, nil
}

func (s *Server) activeRevisionHash(ctx context.Context, id string) (string, error) {
	activations, err := s.store.ListProtocolActivations(ctx)
	if err != nil {
		return "", err
	}
	for _, activation := range activations {
		if activation.ProtocolID == id {
			return activation.RevisionHash, nil
		}
	}
	return "", nil
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
