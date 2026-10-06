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
	defer s.protocolUses.acquire(candidate.binding.ProtocolID, candidate.compiled.Hash())()
	for attempt := 0; ; attempt++ {
		result, err := s.collectProtocolGenerationAttempt(ctx, candidate, request, record, onText)
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

func (s *Server) collectProtocolGenerationAttempt(ctx context.Context, candidate gatewayCandidate, request *protocol.Request, record *usageRecord, onText func(protocol.NodeKind, string)) (*protocol.Response, error) {
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
	options := protocol.EvaluationContext{Scope: candidate.scope, State: protocol.NewEvaluationState(), Diagnostics: &protocol.DiagnosticSink{}}
	body, err := candidate.compiled.EncodeRequest(ctx, request, options)
	if err != nil {
		return nil, err
	}
	if err := candidate.compiled.CheckOperationInput(candidate.operation, body); err != nil {
		return nil, err
	}
	if record != nil {
		record.OutgoingBody = record.sanitizeBody(body)
	}
	response, err := s.protocolTransport.SendProtocolRequest(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Name})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if record != nil {
		record.StatusCode = response.StatusCode
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := protocol.ReadBoundedBody(response.Body, limits.BufferBytes)
		if err != nil {
			return nil, err
		}
		if record != nil {
			record.ProviderResponse = record.sanitizeBody(body)
		}
		result, decodeErr := candidate.compiled.DecodeHTTPFailure(ctx, response.StatusCode, body, options)
		if decodeErr != nil {
			return nil, &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, decodeErr}}
		}
		return result, &upstreamHTTPFailure{&gatewayFailure{response.StatusCode, protocol.CheckGenerationOutcome(result)}}
	}
	if candidate.operation.Transport == protocol.HTTPJSON {
		body, err := protocol.ReadBoundedBody(response.Body, limits.BufferBytes)
		if err != nil {
			return nil, err
		}
		if record != nil {
			record.ProviderResponse = record.sanitizeBody(body)
		}
		result, err := candidate.compiled.DecodeResponse(ctx, body, options)
		if err != nil {
			return nil, err
		}
		if record != nil {
			if err := observeHostedTools(record, candidate.compiled, body); err != nil {
				return nil, err
			}
			updateRecordProtocolUsage(record, result.Usage)
		}
		return result, errors.Join(protocol.CheckGenerationOutcome(result), protocol.IssuesError(protocol.CheckModelResponse(result, candidate.compiled, candidate.binding, candidate.scope)))
	}
	target := protocol.Target{Protocol: candidate.compiled.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: candidate.binding.Capabilities}
	collector, err := protocol.NewResponseCollector(target, limits)
	if err != nil {
		return nil, err
	}
	err = protocol.ReadFrames(ctx, response.Body, candidate.operation, limits.BufferBytes, func(frame protocol.Value, metadata protocol.Object) error {
		if record != nil {
			record.appendStreamEvent(string(frame.Bytes()))
		}
		options.Values = metadata
		decoded, err := candidate.compiled.DecodeFrame(ctx, frame, options)
		if err != nil {
			return err
		}
		if record != nil {
			if err := observeHostedTools(record, candidate.compiled, frame.Bytes()); err != nil {
				return err
			}
		}
		for _, event := range decoded.Events {
			if err := protocol.IssuesError(protocol.CheckModelEvent(event, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
				return err
			}
			if record != nil {
				updateRecordProtocolUsage(record, event.Usage)
				if event.Response != nil {
					updateRecordProtocolUsage(record, event.Response.Usage)
				}
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
