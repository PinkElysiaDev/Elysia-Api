package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

type gatewayFailure struct {
	status int
	cause  error
}

// upstreamFailure holds an error body already encoded by the pinned ingress.
type upstreamFailure struct {
	cause error
	body  []byte
}

func (failure *upstreamFailure) Error() string { return failure.cause.Error() }
func (failure *upstreamFailure) Unwrap() error { return failure.cause }

func (failure *gatewayFailure) Error() string {
	return fmt.Sprintf("HTTP %d: %v", failure.status, failure.cause)
}
func (failure *gatewayFailure) Unwrap() error { return failure.cause }

func gatewayIssue(identity protocol.Identity, code protocol.IssueCode, path, reason string) *protocol.ConversionError {
	return &protocol.ConversionError{Issues: []protocol.ConversionIssue{{Code: code, Severity: protocol.SeverityError, Protocol: identity, Direction: protocol.EncodeRequest, Stage: "routing", Path: path, Reason: reason, Suggestion: "Correct the protocol/model binding or select a compatible model group."}}}
}

func (s *Server) gatewayProtocol(c *gin.Context) {
	if !s.requireProtocolRuntime(c) {
		return
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	view := service.View()
	ingress, exists := view.Pin(c.Param("protocolId"))
	if !exists {
		respondFail(c, http.StatusNotFound, "inactive_protocol", "protocol has no verified active revision")
		return
	}
	if s.serveGatewayJobControl(c, service, ingress) {
		return
	}
	if operation, exists := gatewaySessionOperation(ingress, c.Request.Method, c.Param("path")); exists {
		handshake, err := protocol.ReadSessionHandshake(c.Request, operation)
		if err != nil {
			respondProtocolError(c, err)
			return
		}
		s.serveGatewaySession(c, view, ingress, c.Param("path"), handshake.Bytes())
		return
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, ingress.ResourceLimits().BufferBytes)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	s.serveProtocolRequest(c, view, ingress, c.Param("path"), body)
}

func (s *Server) serveProtocolRequest(c *gin.Context, view protocol.RegistryView, ingress *protocol.Compiled, path string, body []byte) {
	start := time.Now()
	record := s.initUsageRecord(c, start, body, builtin.FormatType(ingress.Identity().Family))
	record.IngressRevision, record.SourceEndpoint, record.SourceFormat = ingress.Hash(), c.Request.URL.Path, ingress.Identity().DefinitionID
	record.RelayMode = "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	isTaskOwned := false
	defer func() {
		if !isTaskOwned {
			s.recordUsage(record)
		}
	}()
	plan, err := s.prepareGatewayPlan(c, view, ingress, path, body, record)
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	if plan.candidates[0].operation.Kind == "submit" {
		isTaskOwned = s.submitGatewayJob(c, record, plan, plan.candidates[0])
		return
	}
	record.Stream = plan.operation.Transport != protocol.HTTPJSON
	estimate := s.estimateProtocolTokens(plan.request)
	if *s.config.GetUsageConfig().EstimateWhenMissing {
		record.Usage.Estimated, record.Usage.EstimatedTokens, record.UsageSource = true, estimate, "protocol_estimate"
	}
	release, err := s.acquireRateLimit(plan.group, estimate)
	if err != nil {
		s.failGateway(c, record, http.StatusTooManyRequests, err)
		return
	}
	defer release()
	defer func() {
		if record.ProtocolUsage != nil {
			usage := record.ProtocolUsage
			var total int64
			if usage.Total != nil {
				total = usage.Total.Count
			} else {
				if usage.Input != nil {
					total += usage.Input.Count
				}
				if usage.Output != nil {
					total += usage.Output.Count
				}
			}
			s.adjustTokenUsage(plan.group.ID, int(total), usageDayKey(start))
		}
	}()
	for index, candidate := range plan.candidates[:maxAttempts(plan.group.MaxRetries, len(plan.candidates))] {
		if err := c.Request.Context().Err(); err != nil {
			s.failGateway(c, record, statusClientClosedRequest, err)
			return
		}
		err := s.forwardGateway(c, record, plan, candidate)
		if err == nil {
			s.affinity.set(record.KeyHash, plan.group.ID, candidate.model.Name, time.Now())
			return
		}
		if c.Writer.Written() {
			s.failGateway(c, record, http.StatusBadGateway, err)
			return
		}
		canRetry := canRetryGeneration(err)
		isLast := index+1 == maxAttempts(plan.group.MaxRetries, len(plan.candidates))
		if !canRetry || isLast {
			s.failGateway(c, record, http.StatusBadGateway, err)
			return
		}
		s.appendRetryEvent(record, index, candidate.model.Name, err.Error())
		if plan.group.RetryInterval > 0 && !waitForRetryOrCancel(c, plan.group.RetryInterval) {
			s.failGateway(c, record, statusClientClosedRequest, c.Request.Context().Err())
			return
		}
	}
}

func (s *Server) forwardGateway(c *gin.Context, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate) error {
	defer s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash())()
	defer s.protocolUses.acquire(record.SourceFormat, record.IngressRevision)()
	prepareCandidateRecord(record, candidate, plan.request)
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return &gatewayFailure{http.StatusForbidden, fmt.Errorf("target base URL rejected: %w", err)}
	}
	request := plan.request.Clone()
	if candidate.prepared != nil {
		request = candidate.prepared.Clone()
	}
	// 上行模型串用 API id；Name 是显示名（Gemini displayName 直接拼路径会 404）。
	request.Model = protocol.StringValue(candidate.model.Identifier())
	options := protocol.EvaluationContext{Scope: candidate.scope, Diagnostics: &protocol.DiagnosticSink{}, ClientOutput: request.ClientOutput}
	if candidate.continuation != nil {
		candidate.continuation.sink = options.Diagnostics
	}
	defer func() { record.appendConversionIssues(options.Diagnostics.Issues()) }()
	if candidate.model.CacheSynthesis {
		protocol.SynthesizeCacheBreakpoints(request, candidate.compiled.Capabilities(protocol.EncodeRequest), options)
	}
	record.SystemStructure.After = systemStructure(request)
	body, err := candidate.compiled.EncodeRequest(c.Request.Context(), request, options)
	if err != nil {
		return &gatewayFailure{http.StatusBadRequest, err}
	}
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		body, err = candidate.conversion.Wire(c.Request.Context(), body, candidate.conversionContext(plan.ingress, false), options.Diagnostics)
		if err != nil {
			return &gatewayFailure{http.StatusBadRequest, err}
		}
		decoded, e := candidate.compiled.DecodeRequest(c.Request.Context(), body, options)
		if e != nil {
			return &gatewayFailure{http.StatusBadRequest, e}
		}
		if e = protocol.IssuesError(protocol.CheckRoute(decoded, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); e != nil {
			return &gatewayFailure{http.StatusBadRequest, e}
		}
	}
	if v, e := protocol.ParseValue(body); e != nil {
		return e
	} else if e = candidate.compiled.ValidateWireOutput(protocol.EncodeRequest, v); e != nil {
		return e
	}
	if err := candidate.compiled.CheckOperationInput(candidate.operation, body); err != nil {
		return &gatewayFailure{http.StatusBadRequest, err}
	}
	setRecordModel(record, candidate.model, builtin.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.OutgoingBody = record.sanitizeBody(body)
	response, err := s.protocolTransport.SendProtocolRequest(c.Request.Context(), candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Identifier()})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	record.TargetEndpoint = response.Request.URL.EscapedPath()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
		if err != nil {
			return err
		}
		record.ProviderResponse = record.sanitizeBody(body)
		return mapGatewayHTTPFailure(c.Request.Context(), record, response.StatusCode, body, plan.ingress, candidate, options)
	}
	if candidate.operation.Transport != protocol.HTTPJSON {
		return s.forwardGatewayStream(c, record, plan, candidate, response)
	}
	body, err = protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
	if err != nil {
		return err
	}
	record.ProviderResponse = record.sanitizeBody(body)
	semantic, err := candidate.compiled.DecodeResponse(c.Request.Context(), body, options)
	if err != nil {
		return err
	}
	if err := observeHostedTools(record, candidate.compiled, body); err != nil {
		return err
	}
	if err := protocol.ValidateUsageArithmetic(semantic.Usage); err != nil {
		return err
	}
	updateRecordProtocolUsage(record, semantic.Usage)
	if err := protocol.CheckGenerationOutcome(semantic); err != nil {
		return encodeGatewayFailure(c.Request.Context(), http.StatusBadGateway, semantic, plan.ingress, options)
	}
	if err := protocol.IssuesError(protocol.CheckModelResponse(semantic, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
		return err
	}
	if err := s.captureContinuationResponse(c, candidate.continuation, semantic); err != nil {
		return err
	}
	if candidate.conversion != nil {
		semantic, err = candidate.conversion.Response(c.Request.Context(), semantic, candidate.conversionContext(plan.ingress, true), options.Diagnostics)
		if err != nil {
			return err
		}
	}
	if !semantic.Model.IsZero() {
		semantic.Model = plan.request.Model
	}
	body, err = plan.ingress.EncodeResponse(c.Request.Context(), semantic, options)
	if err != nil {
		return err
	}
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		body, err = candidate.conversion.Wire(c.Request.Context(), body, candidate.conversionContext(plan.ingress, true), options.Diagnostics)
		if err != nil {
			return err
		}
		// The target decoder applies its declared structure and capabilities.
		if _, err = plan.ingress.DecodeResponse(c.Request.Context(), body, options); err != nil {
			return err
		}
	}
	if candidate.continuation != nil {
		body, err = builtin.AttachContinuationCarriers(body, plan.ingress.Identity().Family, candidate.continuation.tokens)
		if err != nil {
			return err
		}
	}
	wireValue, parseErr := protocol.ParseValue(body)
	if parseErr != nil {
		return parseErr
	}
	if err := plan.ingress.ValidateWireOutput(protocol.EncodeResponse, wireValue); err != nil {
		return err
	}
	if len(body) > plan.ingress.ResourceLimits().BufferBytes {
		return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/response", Reason: "response including continuation carriers exceeds target buffer limit"}})
	}
	record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
	c.Data(http.StatusOK, "application/json", body)
	return nil
}

func (s *Server) failGateway(c *gin.Context, record *usageRecord, status int, err error) {
	var failure *gatewayFailure
	if errors.As(err, &failure) {
		status = failure.status
	}
	if c.Request.Context().Err() != nil {
		status = statusClientClosedRequest
	}
	record.StatusCode, record.Error, record.ErrorKind = status, err.Error(), ErrorKindUpstream
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		record.ConversionIssues = conversion.Issues
		if len(conversion.Issues) > 0 {
			record.ErrorKind = string(conversion.Issues[0].Code)
		}
	}
	if status == statusClientClosedRequest {
		record.ErrorKind = ErrorKindClientCanceled
	}
	if c.Writer.Written() {
		return
	}
	var upstream *upstreamFailure
	if status != statusClientClosedRequest && errors.As(err, &upstream) {
		c.Data(status, "application/json", upstream.body)
		return
	}
	if !strings.HasPrefix(c.Request.URL.Path, "/gateway/") {
		var wireError *builtin.GatewayError
		if !errors.As(err, &wireError) {
			wireError = &builtin.GatewayError{Class: builtin.ClassFromStatus(status), Status: status, Message: err.Error()}
		}
		writeProtocolError(c, inputFormatFromPath(c.Request.URL.Path), wireError)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"code": "protocol_gateway_error", "message": err.Error(), "issues": record.ConversionIssues}})
}
