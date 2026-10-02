package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

type gatewayFailure struct {
	status int
	cause  error
}

func (failure *gatewayFailure) Error() string { return failure.cause.Error() }
func (failure *gatewayFailure) Unwrap() error { return failure.cause }

func gatewayIssue(identity protocol.Identity, code protocol.IssueCode, path, reason string) *protocol.ConversionError {
	return &protocol.ConversionError{Issues: []protocol.ConversionIssue{{Code: code, Severity: protocol.SeverityError, Protocol: identity, Direction: protocol.EncodeRequest, Stage: "routing", Path: path, Reason: reason, Suggestion: "Correct the protocol/model binding or select a compatible model group."}}}
}

func (s *Server) gatewayProtocol(c *gin.Context) {
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
	if operation, exists := gatewaySessionOperation(ingress, c.Request.Method, c.Param("path")); exists {
		handshake, err := protocol.ReadSessionHandshake(c.Request, operation)
		if err != nil {
			respondProtocolError(c, err)
			return
		}
		s.serveGatewaySession(c, view, ingress, c.Param("path"), handshake.Bytes())
		return
	}
	body, err := protocol.ReadBoundedBody(c.Request.Body, protocol.DefaultLimits().BufferBytes)
	if err != nil {
		respondFail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	s.serveProtocolRequest(c, view, ingress, c.Param("path"), body)
}

func (s *Server) serveProtocolRequest(c *gin.Context, view protocol.RegistryView, ingress *protocol.Compiled, path string, body []byte) {
	start := time.Now()
	record := s.initUsageRecord(c, start, body, relay.FormatType(ingress.Identity().Family))
	record.IngressRevision, record.SourceEndpoint, record.SourceFormat = ingress.Hash(), c.Request.URL.Path, ingress.Identity().DefinitionID
	record.RelayMode = "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	defer s.recordUsage(record)
	plan, err := s.prepareGatewayPlan(c, view, ingress, path, body, record)
	if err != nil {
		s.failGateway(c, record, http.StatusBadRequest, err)
		return
	}
	record.Stream = plan.operation.Transport != protocol.HTTPJSON
	estimate := s.estimateProtocolTokens(plan.request)
	record.Usage.Estimated, record.Usage.EstimatedTokens, record.UsageSource = true, estimate, "protocol_estimate"
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
			s.adjustTokenUsage(plan.group.ID, int(total), start.Format("2006-01-02"))
		}
	}()
	for index, candidate := range plan.candidates[:maxAttempts(plan.group.MaxRetries, len(plan.candidates))] {
		if err := c.Request.Context().Err(); err != nil {
			s.failGateway(c, record, 499, err)
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
		var failure *gatewayFailure
		canRetry := errors.As(err, &failure) && (failure.status == http.StatusTooManyRequests || failure.status == http.StatusServiceUnavailable)
		isLast := index+1 == maxAttempts(plan.group.MaxRetries, len(plan.candidates))
		if !canRetry || isLast {
			s.failGateway(c, record, http.StatusBadGateway, err)
			return
		}
		s.appendRetryEvent(record, index, candidate.model.Name, err.Error())
		if plan.group.RetryInterval > 0 && !waitForRetryOrCancel(c, plan.group.RetryInterval) {
			s.failGateway(c, record, 499, c.Request.Context().Err())
			return
		}
	}
}

func (s *Server) forwardGateway(c *gin.Context, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate) error {
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return &gatewayFailure{http.StatusForbidden, fmt.Errorf("target base URL rejected: %w", err)}
	}
	request := *plan.request
	request.Model = protocol.StringValue(candidate.model.Name)
	options := protocol.EvaluationContext{Scope: candidate.scope}
	body, err := candidate.compiled.EncodeRequest(c.Request.Context(), &request, options)
	if err != nil {
		return err
	}
	setRecordModel(record, candidate.model, relay.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.OutgoingBody = record.sanitizeBody(body)
	response, err := s.openaiAdapter.SendProtocolRequest(c.Request.Context(), candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Name})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := protocol.ReadBoundedBody(response.Body, protocol.DefaultLimits().BufferBytes)
		if err != nil {
			return err
		}
		record.ProviderResponse = record.sanitizeBody(body)
		return &gatewayFailure{response.StatusCode, fmt.Errorf("upstream returned HTTP %d", response.StatusCode)}
	}
	if candidate.operation.Transport != protocol.HTTPJSON {
		return s.forwardGatewayStream(c, record, plan, candidate, response)
	}
	body, err = protocol.ReadBoundedBody(response.Body, protocol.DefaultLimits().BufferBytes)
	if err != nil {
		return err
	}
	record.ProviderResponse = record.sanitizeBody(body)
	semantic, err := candidate.compiled.DecodeResponse(c.Request.Context(), body, options)
	if err != nil {
		return err
	}
	updateRecordProtocolUsage(record, semantic.Usage)
	if err := protocol.IssuesError(protocol.CheckModelResponse(semantic, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
		return err
	}
	if !semantic.Model.IsZero() {
		semantic.Model = plan.request.Model
	}
	body, err = plan.ingress.EncodeResponse(c.Request.Context(), semantic, options)
	if err != nil {
		return err
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
		status = 499
	}
	record.StatusCode, record.Error, record.ErrorKind = status, err.Error(), ErrorKindUpstream
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		record.ConversionIssues = conversion.Issues
		if len(conversion.Issues) > 0 {
			record.ErrorKind = string(conversion.Issues[0].Code)
		}
	}
	if status == 499 {
		record.ErrorKind = ErrorKindClientCanceled
	}
	if c.Writer.Written() {
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"code": "protocol_gateway_error", "message": err.Error(), "issues": record.ConversionIssues}})
}
