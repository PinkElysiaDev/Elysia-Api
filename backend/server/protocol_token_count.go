package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/internal/routing"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// Count requests use the pinned operation, route permissions and transport, but
// never enter generation estimation, response collection or usage settlement.
func (s *Server) serveTokenCounting(ctx context.Context, c *app.RequestContext, selection *ingressSelection) bool {
	var name string
	var operation protocol.Operation
	var parameters map[string]string
	for candidate, route := range selection.compiled.OperationRoutes() {
		if route.Kind != "count_tokens" || route.Method != string(c.Method()) {
			continue
		}
		values, matches := protocol.MatchOperationPath(route.Path, selection.path)
		if !matches {
			continue
		}
		if name != "" {
			s.writeTokenCountError(ctx, c, http.StatusBadRequest, gatewayIssue(selection.compiled.Identity(), protocol.InvalidDefinition, "/operations", "client path selects multiple token counting operations"))
			return true
		}
		name, parameters = candidate, values
		operation, _ = selection.compiled.Operation(candidate)
	}
	if name == "" {
		return false
	}
	var ok bool
	ctx, ok = s.pinProtocolRequest(ctx, c)
	if !ok {
		return true
	}
	defer s.releaseGatewayPreparation(c)
	body, err := s.readPreparedProtocolBody(ctx, c, selection.compiled)
	if err != nil {
		s.writeTokenCountError(ctx, c, http.StatusBadRequest, protocol.AnnotateResourceError(err, selection.compiled.Identity(), protocol.DecodeRequest, "read.request"))
		return true
	}
	// Route preparation only needs request identity and affinity. This transient
	// record owns no body capture, usage estimate, token debit or persisted log.
	started := time.Now()
	record := &usageRecord{StartedAt: started, RequestID: usageRequestID(started), KeyHash: c.GetString("elysiaKeyHash")}
	plan := &gatewayPlan{ingress: selection.compiled, operation: operation, operationName: name, bodyBytes: len(body)}
	defer plan.releasePreparation()
	conversions, err := s.prepareTokenCountPlan(ctx, c, selection, plan, parameters, body, record)
	if err != nil {
		s.writeTokenCountError(ctx, c, http.StatusBadRequest, err)
		return true
	}
	release, err := s.limiter.Acquire(plan.group, 0)
	if err != nil {
		s.writeTokenCountError(ctx, c, http.StatusTooManyRequests, err)
		return true
	}
	defer release()
	defer s.protocolUses.acquire(selection.compiled.Identity().DefinitionID, selection.compiled.Hash())()
	maxAttempts := routing.MaxAttempts(plan.group.MaxRetries, len(plan.candidates))
	attempts := 0
	var issues []protocol.ConversionIssue
	var lastFailure error
	for _, candidate := range plan.candidates {
		if err := s.acquireGatewayPreparation(ctx, c, int64(plan.bodyBytes)); err != nil {
			s.writeTokenCountError(ctx, c, preparationStatus(err), err)
			return true
		}
		wire, err := s.prepareTokenCountCandidate(ctx, plan, conversions, &candidate, record)
		if err != nil {
			var failure *protocol.ConversionError
			if ctx.Err() == nil && errors.As(err, &failure) {
				issues = append(issues, failure.Issues...)
				continue
			}
			s.writeTokenCountError(ctx, c, http.StatusBadRequest, err)
			return true
		}
		s.releaseGatewayPreparation(c)
		attempts++
		result, err := s.forwardTokenCount(ctx, plan, candidate, wire)
		if err == nil {
			s.affinity.set(record.KeyHash, plan.group.ID, candidate.model.Name)
			c.Data(http.StatusOK, contentTypeJSON, result.Bytes())
			return true
		}
		lastFailure = err
		if !canRetryGeneration(err) || attempts == maxAttempts {
			s.writeTokenCountError(ctx, c, http.StatusBadGateway, err)
			return true
		}
		if !waitForRetryOrCancel(ctx, plan.group.RetryInterval) {
			s.writeTokenCountError(ctx, c, statusClientClosedRequest, ctx.Err())
			return true
		}
	}
	if lastFailure != nil {
		s.writeTokenCountError(ctx, c, http.StatusBadGateway, lastFailure)
	} else {
		s.writeTokenCountError(ctx, c, http.StatusBadRequest, protocol.IssuesError(issues))
	}
	return true
}

func (s *Server) prepareTokenCountPlan(ctx context.Context, c *app.RequestContext, selection *ingressSelection, plan *gatewayPlan, parameters map[string]string, body []byte, record *usageRecord) (*gatewayConversionSnapshot, error) {
	conversions, err := s.pinGatewayConversions(ctx)
	if err != nil {
		return nil, err
	}
	decoder, _ := plan.ingress.TokenCountRequestProtocol(plan.operationName)
	options, err := (protocol.EvaluationContext{Values: protocol.Object{"path": protocol.StringValue(selection.path)}}).WithOperation(record.RequestID, record.StartedAt.Unix())
	if err != nil {
		return nil, err
	}
	for name, value := range parameters {
		options.Values[name] = protocol.StringValue(value)
	}
	options.ResolveRequestScope = func(request *protocol.Request) (protocol.Scope, error) {
		return s.bindGatewayRequest(ctx, c, selection.view, conversions.bindings, plan, selection.path, request, record)
	}
	plan.request, err = decoder.DecodeRequest(ctx, body, options)
	return conversions, err
}

func (s *Server) prepareTokenCountCandidate(ctx context.Context, plan *gatewayPlan, conversions *gatewayConversionSnapshot, candidate *gatewayCandidate, record *usageRecord) (protocol.Value, error) {
	decoder, _ := plan.ingress.TokenCountRequestProtocol(plan.operationName)
	encoder, _ := candidate.compiled.TokenCountRequestProtocol(candidate.operationName)
	candidate.binding = constrainGatewayCapabilities(candidate.binding, candidate.model, plan.group, conversions.bindings)
	var err error
	candidate.conversion, err = conversions.resolve(decoder, encoder, candidate.model, candidate.operationName, protocol.HTTPJSON)
	if err != nil {
		return protocol.Value{}, err
	}
	if err := s.loadProviderConversionEvidence(ctx, candidate.conversion, candidate.model, candidate.compiled); err != nil {
		return protocol.Value{}, err
	}
	options, err := (protocol.EvaluationContext{Scope: candidate.scope}).WithOperation(record.RequestID, record.StartedAt.Unix())
	if err != nil {
		return protocol.Value{}, err
	}
	return candidate.prepareTokenCountWire(ctx, plan.ingress, encoder, plan.request, options)
}

func (candidate gatewayCandidate) prepareTokenCountWire(ctx context.Context, ingress, encoder *protocol.Compiled, request *protocol.Request, options protocol.EvaluationContext) (protocol.Value, error) {
	route := candidate.conversionContext(ingress, false).WithOperation(options)
	prepared, err := candidate.conversion.Request(ctx, request, route, nil)
	if err != nil {
		return protocol.Value{}, err
	}
	prepared.Model = protocol.StringValue(candidate.model.Identifier())
	options.ClientOutput = prepared.ClientOutput
	wire, err := encoder.EncodeRequestValue(ctx, prepared, options)
	if err != nil {
		return protocol.Value{}, err
	}
	return candidate.finalizeRequestWire(ctx, wire, route, options)
}

func (s *Server) forwardTokenCount(ctx context.Context, plan *gatewayPlan, candidate gatewayCandidate, wire protocol.Value) (protocol.Value, error) {
	defer s.protocolUses.acquire(candidate.compiled.Identity().DefinitionID, candidate.compiled.Hash())()
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return protocol.Value{}, &gatewayFailure{http.StatusForbidden, fmt.Errorf("target base URL rejected: %w", err)}
	}
	response, err := s.protocolTransport.SendProtocolRequest(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, wire.Bytes(), map[string]string{"model": candidate.model.Identifier()})
	if err != nil {
		return protocol.Value{}, err
	}
	defer response.Body.Close()
	body, err := protocol.ReadBoundedBody(response.Body, candidate.compiled.ResourceLimits().BufferBytes)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if err != nil {
			return protocol.Value{}, &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, attachFailureDiagnostics(protocol.CheckGenerationOutcome(fallbackHTTPFailure(response.StatusCode)), err)}}
		}
		failure, secondary := decodeUpstreamHTTPFailure(ctx, response.StatusCode, body, candidate.compiled, protocol.EvaluationContext{})
		projected := encodeGatewayFailure(ctx, response.StatusCode, failure, plan.ingress, protocol.EvaluationContext{})
		projected.cause = attachFailureDiagnostics(projected.cause, secondary)
		return protocol.Value{}, &upstreamHTTPFailure{projected}
	}
	if err != nil {
		return protocol.Value{}, err
	}
	input, err := protocol.ParseValue(body)
	if err != nil {
		return protocol.Value{}, err
	}
	count, err := candidate.compiled.DecodeTokenCount(ctx, candidate.operationName, input)
	if err != nil {
		return protocol.Value{}, err
	}
	output, err := plan.ingress.EncodeTokenCount(ctx, plan.operationName, count)
	if err != nil {
		return protocol.Value{}, &gatewayFailure{http.StatusInternalServerError, err}
	}
	return output, nil
}

func (s *Server) writeTokenCountError(ctx context.Context, c *app.RequestContext, status int, err error) {
	var failure *gatewayFailure
	if errors.As(err, &failure) {
		status = failure.status
	}
	if ctx.Err() != nil {
		status, err = statusClientClosedRequest, ctx.Err()
	}
	var upstream *upstreamFailure
	if errors.As(err, &upstream) {
		c.Data(status, contentTypeJSON, upstream.body)
		return
	}
	s.writeIngressError(ctx, c, &builtin.GatewayError{Class: builtin.ClassFromStatus(status), Status: status, Message: err.Error()})
}
