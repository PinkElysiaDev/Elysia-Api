package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/protocol"
)

var generationRetryDelays = [...]time.Duration{time.Second, 3 * time.Second}

// isRetryableGenerationStatus only handles explicit HTTP failures before any
// stream output. Transport errors have uncertain submission status and stop.
func isRetryableGenerationStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// collectProtocolGeneration is the bounded consumer used by Agent and live
// probes. It shares codecs, contract checks and stream replay with the gateway.
func (s *Server) collectProtocolGeneration(ctx context.Context, candidate gatewayCandidate, request *protocol.Request, record *usageRecord, onText func(protocol.NodeKind, string)) (*protocol.Response, error) {
	ctx, budgetErr := s.pinProtocolBudget(ctx)
	if budgetErr != nil {
		return nil, budgetErr
	}
	defer s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash())()
	for attempt := 0; ; attempt++ {
		options, err := generationOperationContext(record)
		if err != nil {
			return nil, err
		}
		result, err := s.collectProtocolGenerationAttempt(ctx, candidate, request, record, onText, options)
		if err == nil || ctx.Err() != nil || attempt == len(generationRetryDelays) || !canRetryGeneration(err) {
			return result, err
		}
		timer := time.NewTimer(generationRetryDelays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if record != nil {
			s.appendRetryEvent(record, attempt+1, candidate.model.Name, err.Error())
			record.ProviderResponse = usageBody{}
		}
	}
}

// Bind a real generation attempt before any preparation that executes Mapping.
func generationOperationContext(record *usageRecord) (protocol.EvaluationContext, error) {
	if record != nil {
		return (protocol.EvaluationContext{}).WithOperation(record.RequestID, record.StartedAt.Unix())
	}
	started := time.Now()
	return (protocol.EvaluationContext{}).WithOperation(usageRequestID(started), started.Unix())
}

func (s *Server) collectProtocolGenerationAttempt(ctx context.Context, candidate gatewayCandidate, request *protocol.Request, record *usageRecord, onText func(protocol.NodeKind, string), options protocol.EvaluationContext) (*protocol.Response, error) {
	if record != nil {
		record.UpstreamHTTPStatus = 0
		record.TransportFailure = nil
	}
	limits := candidate.compiled.ResourceLimits()
	if err := protocol.IssuesError(protocol.CheckRoute(request, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); err != nil {
		return nil, err
	}
	if candidate.operation.Kind != "generate" || candidate.operation.Transport == protocol.WebSocket {
		return nil, fmt.Errorf("generation collection requires an HTTP JSON, SSE or NDJSON generate operation")
	}
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return nil, err
	}
	if record != nil {
		s.estimateGatewayAttempt(record, request)
	}
	options.InterleavedThinking, options.Scope = protocol.OperationInterleavedThinking(candidate.operation), candidate.scope
	options.State, options.Diagnostics = protocol.NewEvaluationState(), &protocol.DiagnosticSink{}
	options.ClientOutput = request.ClientOutput
	options = options.WithConversion(candidate.conversion, candidate.conversionContext(candidate.compiled, true).WithOperation(options))
	defer options.State.Release()
	defer func() {
		if record != nil {
			record.appendConversionIssues(options.Diagnostics.Issues())
		}
	}()
	wire, err := candidate.compiled.EncodeRequestValue(ctx, request, options)
	if err != nil {
		return nil, err
	}
	wire, err = candidate.finalizeRequestWire(ctx, wire, candidate.conversionContext(candidate.compiled, false).WithOperation(options), options)
	if err != nil {
		return nil, err
	}
	if record != nil {
		record.OutgoingBody = record.sanitizeBodyText(wire.JSONString())
	}
	body := wire.Bytes()
	outboundBody := body
	response, err := s.protocolTransport.SendProtocolRequest(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Identifier()})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if record != nil {
		record.StatusCode = response.StatusCode
		record.UpstreamHTTPStatus = response.StatusCode
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := protocol.ReadBoundedBody(response.Body, limits.BufferBytes)
		if err != nil {
			return nil, &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, attachFailureDiagnostics(protocol.CheckGenerationOutcome(fallbackHTTPFailure(response.StatusCode)), protocol.AnnotateResourceError(err, candidate.compiled.Identity(), protocol.DecodeResponse, "read.response"))}}
		}
		if record != nil {
			record.ProviderResponse = record.sanitizeBody(body)
		}
		result, secondary := decodeUpstreamHTTPFailure(ctx, response.StatusCode, body, candidate.compiled, options)
		if record != nil {
			updateRecordProtocolUsage(record, result.Usage)
			var conversion *protocol.ConversionError
			if errors.As(secondary, &conversion) {
				record.appendConversionIssues(conversion.Issues)
			}
		}
		return result, &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, attachFailureDiagnostics(protocol.CheckGenerationOutcome(result), secondary)}}
	}
	if candidate.operation.Transport == protocol.HTTPJSON {
		body, err := protocol.ReadBoundedBody(response.Body, limits.BufferBytes)
		if err != nil {
			return nil, protocol.AnnotateResourceError(err, candidate.compiled.Identity(), protocol.DecodeResponse, "read.response")
		}
		if record != nil {
			record.ProviderResponse = record.sanitizeBody(body)
		}
		result, err := candidate.compiled.DecodeResponse(ctx, body, options)
		if err != nil {
			return nil, err
		}
		if record != nil {
			if candidate.compiled.CoreVersion() != protocol.ProtocolCoreVersion {
				if err := observeLegacyHostedTools(record, candidate.compiled, body); err != nil {
					return nil, err
				}
			}
			updateRecordProtocolUsage(record, result.Usage)
		}
		route, err := candidate.conversionContext(candidate.compiled, true).WithOperation(options).WithResponseModelOrigin(candidate.compiled, candidate.compiled, outboundBody, body)
		if err != nil {
			return nil, err
		}
		result, err = candidate.conversion.RepairCollectedResponseModel(ctx, result, route, options.Diagnostics)
		if err != nil {
			return nil, err
		}
		if err := candidate.compiled.ValidateResponseReferences(ctx, result); err != nil {
			return nil, err
		}
		return result, errors.Join(protocol.CheckGenerationOutcome(result), protocol.IssuesError(protocol.CheckModelResponse(result, candidate.compiled, candidate.binding, candidate.scope)))
	}
	target := candidate.sourceEventTarget()
	collector, err := protocol.NewResponseCollector(target, limits)
	if err != nil {
		return nil, err
	}
	source, err := newProtocolSourceStream(ctx, candidate, candidate.compiled, candidate.conversionContext(candidate.compiled, true), options, record, func(decoded *protocol.EventFrame, _ protocol.Object) error {
		for _, event := range decoded.Events {
			if err := candidate.compiled.ValidateEventReferences(ctx, event, options); err != nil {
				return err
			}
			kind, delta, err := collector.Consume(event)
			if err != nil {
				return err
			}
			if onText != nil && delta != "" {
				onText(kind, delta)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = protocol.ReadFrames(ctx, response.Body, candidate.operation, limits.BufferBytes, source.Push)
	err = source.Finish(err)
	if err != nil && source.terminalFailure != nil {
		err = errors.Join(source.terminalFailure, err)
	}
	if err == nil {
		var result *protocol.Response
		result, err = collector.Finish()
		if err == nil {
			return result, nil
		}
	}
	partial, snapshotErr := collector.Partial()
	return partial, errors.Join(err, snapshotErr)
}
