package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/elysia-api/backend/internal/routing"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
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

func (s *Server) gatewayProtocol(ctx context.Context, c *app.RequestContext) {
	if !s.requireProtocolRuntime(ctx, c) {
		return
	}
	service, err := s.protocolService()
	if err != nil {
		respondProtocolError(ctx, c, err)
		return
	}
	view := service.View()
	ingress, exists := view.Pin(c.Param("protocolId"))
	if !exists {
		respondFail(ctx, c, http.StatusNotFound, "inactive_protocol", "protocol has no verified active revision")
		return
	}
	if s.serveGatewayJobControl(ctx, c, service, ingress) {
		return
	}
	path := "/" + strings.TrimPrefix(c.Param("path"), "/")
	if operation, exists := gatewaySessionOperation(ingress, string(c.Method()), path); exists {
		handshake, err := readSessionHandshake(c, operation)
		if err != nil {
			respondProtocolError(ctx, c, err)
			return
		}
		s.serveGatewaySession(ctx, c, view, ingress, path, handshake.Bytes())
		return
	}
	defer s.releaseGatewayPreparation(c)
	body, ok := s.readGatewayRequest(ctx, c, ingress)
	if !ok {
		return
	}
	s.serveProtocolRequest(ctx, c, view, ingress, path, body)
}

func (s *Server) serveProtocolRequest(ctx context.Context, c *app.RequestContext, view protocol.RegistryView, ingress *protocol.Compiled, path string, body []byte) {
	var budgetOK bool
	ctx, budgetOK = s.pinProtocolRequest(ctx, c)
	if !budgetOK {
		return
	}
	if err := s.acquireGatewayPreparation(ctx, c, int64(len(body))); err != nil {
		s.rejectPreparation(ctx, c, ingress, err)
		return
	}
	defer s.releaseGatewayPreparation(c)
	start := time.Now()
	record := s.initUsageRecord(ctx, c, start, body, builtin.FormatType(ingress.Identity().Family))
	record.IngressRevision, record.SourceEndpoint, record.SourceFormat = ingress.Hash(), string(c.Path()), ingress.Identity().DefinitionID
	record.RelayMode = "protocol_v2"
	installDownstreamCapture(c, record, downstreamCaptureLimit(s.usageLogConfig()))
	isTaskOwned := false
	defer func() {
		if !isTaskOwned {
			s.recordHTTPUsage(c, record)
		}
	}()
	plan, err := s.prepareGatewayPlan(ctx, c, view, ingress, path, body, record)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusBadRequest, err)
		return
	}
	if plan.candidates[0].operation.Kind == "submit" {
		s.releaseGatewayPreparation(c) // Task workers have their own lifetime/admission.
		isTaskOwned = s.submitGatewayJob(ctx, c, record, plan, plan.candidates[0])
		return
	}
	record.Stream = plan.operation.Transport != protocol.HTTPJSON
	estimate := s.estimateGatewayReservation(plan)
	if *s.config.GetUsageConfig().EstimateWhenMissing {
		record.Usage.Estimated, record.Usage.EstimatedTokens, record.UsageSource = true, estimate, "protocol_estimate"
	}
	release, err := s.limiter.Acquire(plan.group, estimate)
	if err != nil {
		s.failGateway(ctx, c, record, http.StatusTooManyRequests, err)
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
			s.limiter.Adjust(plan.group.ID, int(total), usageDayKey(start))
		}
	}()
	attempts := routing.MaxAttempts(plan.group.MaxRetries, len(plan.candidates))
	defer plan.releasePreparation()
	var lastFailure error
	for index := range attempts {
		if err := ctx.Err(); err != nil {
			if lastFailure != nil {
				err = lastFailure
			}
			s.failGateway(ctx, c, record, statusClientClosedRequest, err)
			return
		}
		candidate := plan.takeCandidate(index, attempts)
		modelName := candidate.model.Name
		err := s.forwardGateway(ctx, c, record, plan, candidate)
		if err == nil {
			s.affinity.set(record.KeyHash, plan.group.ID, modelName)
			return
		}
		lastFailure = err
		if responseCommitted(c) {
			s.failGateway(ctx, c, record, http.StatusBadGateway, err)
			return
		}
		canRetry := canRetryGeneration(err)
		isLast := index+1 == attempts
		if !canRetry || isLast {
			s.failGateway(ctx, c, record, http.StatusBadGateway, err)
			return
		}
		s.appendRetryEvent(record, index, modelName, err.Error())
		if plan.group.RetryInterval > 0 && !waitForRetryOrCancel(ctx, plan.group.RetryInterval) {
			s.failGateway(ctx, c, record, statusClientClosedRequest, err)
			return
		}
	}
}

func (s *Server) forwardGateway(ctx context.Context, c *app.RequestContext, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate) error {
	// A retry reacquires preparation capacity; no lease spans provider I/O or
	// retry backoff. The initial attempt reuses the ingress/preflight lease.
	if err := s.acquireGatewayPreparation(ctx, c, int64(plan.bodyBytes)); err != nil {
		if preparationStatus(err) == http.StatusServiceUnavailable {
			c.Header("Retry-After", "1")
		}
		return &gatewayFailure{preparationStatus(err), err}
	}
	defer s.releaseGatewayPreparation(c)
	// Capture the current attempt variable, not a value copy retaining history.
	defer func() { candidate.releaseRequestEncoding() }()
	record.UpstreamHTTPStatus = 0
	record.TransportFailure = nil
	defer s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash())()
	defer s.protocolUses.acquire(record.SourceFormat, record.IngressRevision)()
	prepareCandidateRecord(record, candidate, plan.request)
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return &gatewayFailure{http.StatusForbidden, fmt.Errorf("target base URL rejected: %w", err)}
	}
	prepared := plan.request
	if candidate.prepared != nil {
		prepared = candidate.prepared
	}
	request := prepared.Clone()
	// 上行模型串用 API id；Name 是显示名（Gemini displayName 直接拼路径会 404）。
	request.Model = protocol.StringValue(candidate.model.Identifier())
	options := protocol.EvaluationContext{InterleavedThinking: protocol.OperationInterleavedThinking(candidate.operation), Scope: candidate.scope, Diagnostics: &protocol.DiagnosticSink{}, ClientOutput: request.ClientOutput}
	delivery := plan.responsePlan(options.ClientOutput)
	if candidate.continuation != nil {
		candidate.continuation.sink = options.Diagnostics
	}
	defer func() { record.appendConversionIssues(options.Diagnostics.Issues()) }()
	if candidate.model.CacheSynthesis {
		protocol.SynthesizeCacheBreakpoints(request, candidate.compiled.Capabilities(protocol.EncodeRequest), options)
	}
	s.estimateGatewayAttempt(record, request)
	record.SystemStructure.After = systemStructure(request)
	wireValue, isPrepared := candidate.takeRequestValue(ctx, request, options)
	var err error
	if !isPrepared {
		wireValue, err = candidate.compiled.EncodeRequestValue(ctx, request, options)
		if err != nil {
			return &gatewayFailure{http.StatusBadRequest, err}
		}
	}
	if candidate.conversion.HasPhase(protocol.ConversionWire) {
		wireValue, err = candidate.conversion.ApplyValue(ctx, protocol.ConversionWire, wireValue, candidate.conversionContext(plan.ingress, false), options.Diagnostics)
		if err != nil {
			return &gatewayFailure{http.StatusBadRequest, err}
		}
		decoded, e := candidate.compiled.DecodeRequestValue(ctx, wireValue, options)
		if e != nil {
			return &gatewayFailure{http.StatusBadRequest, e}
		}
		if e = protocol.IssuesError(protocol.CheckRoute(decoded, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); e != nil {
			return &gatewayFailure{http.StatusBadRequest, e}
		}
	}
	if e := candidate.compiled.CheckRequestBody(ctx, wireValue); e != nil {
		return &gatewayFailure{http.StatusBadRequest, e}
	} else if e = candidate.compiled.ValidateWireOutput(protocol.EncodeRequest, wireValue); e != nil {
		return e
	}
	if err := candidate.compiled.CheckOperationValueInput(ctx, candidate.operationName, wireValue); err != nil {
		return &gatewayFailure{http.StatusBadRequest, err}
	}
	responseRoute, err := candidate.conversionContext(plan.ingress, true).WithRequestModelOrigin(plan.ingress, candidate.compiled, wireValue)
	if err != nil {
		return err
	}
	setRecordModel(record, candidate.model, builtin.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.OutgoingBody = record.sanitizeBodyText(wireValue.JSONString())
	// Keep transport storage out of the log cleaning lifetime.
	body := wireValue.Bytes()
	candidate.prepared = nil
	candidate.releaseRequestEncoding()
	candidate.encoding = nil
	if candidate.finalAttempt {
		plan.releasePreparation()
	}
	s.releaseGatewayPreparation(c)
	response, err := s.protocolTransport.SendProtocolRequest(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Identifier()})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	record.UpstreamHTTPStatus = response.StatusCode
	record.TargetEndpoint = response.Request.URL.EscapedPath()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
		if err != nil {
			return &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, attachFailureDiagnostics(protocol.CheckGenerationOutcome(fallbackHTTPFailure(response.StatusCode, nil)), protocol.AnnotateResourceError(err, candidate.compiled.Identity(), protocol.DecodeResponse, "read.response"))}}
		}
		record.ProviderResponse = record.sanitizeBody(body)
		return mapGatewayHTTPFailure(ctx, record, response.StatusCode, body, plan.ingress, candidate, options)
	}
	// Successful provider headers close the retry lifetime even if later response
	// conversion fails. Stream/response consumers receive only delivery metadata.
	plan.releasePreparation()
	if candidate.operation.Transport != protocol.HTTPJSON {
		return s.forwardGatewayStream(ctx, c, record, delivery, candidate, response)
	}
	return s.forwardGatewayResponse(ctx, c, record, delivery, candidate, response, responseRoute, options)
}

func (s *Server) failGateway(ctx context.Context, c *app.RequestContext, record *usageRecord, status int, err error) {
	var failure *gatewayFailure
	if errors.As(err, &failure) {
		status = failure.status
	}
	if localStatus := localResourceStatus(record, err); localStatus != 0 {
		status = localStatus
	}
	record.StatusCode, record.Error, record.ErrorKind = status, err.Error(), ErrorKindUpstream
	recordFailureEvidence(record, err)
	if isPreparationFailure(err) && record.UpstreamHTTPStatus == 0 {
		record.ErrorKind = "preparation_overloaded"
	}
	class := generationFailureClass(err)
	if class != "" {
		record.ErrorKind = string(class)
		if failure == nil && status != statusClientClosedRequest {
			status, record.StatusCode = class.HTTPStatus(), class.HTTPStatus()
		}
	}
	if status == statusClientClosedRequest {
		record.ErrorKind = ErrorKindClientCanceled
	}
	classifyRequestInterruption(record, ctx, err)
	status = record.StatusCode
	if responseCommitted(c) {
		return
	}
	discardUncommittedStream(c)
	var upstream *upstreamFailure
	if status != statusClientClosedRequest && errors.As(err, &upstream) {
		c.Data(status, "application/json", upstream.body)
		return
	}
	if !strings.HasPrefix(string(c.Path()), "/gateway/") {
		var wireError *builtin.GatewayError
		if !errors.As(err, &wireError) {
			wireError = &builtin.GatewayError{Class: builtin.ClassFromStatus(status), Status: status, Message: err.Error()}
			if class != "" {
				wireError.Class = class
			}
		}
		writeProtocolError(ctx, c, inputFormatFromPath(string(c.Path())), wireError)
		return
	}
	c.JSON(status, utils.H{"error": utils.H{"code": "protocol_gateway_error", "message": err.Error(), "issues": record.ConversionIssues}})
}
