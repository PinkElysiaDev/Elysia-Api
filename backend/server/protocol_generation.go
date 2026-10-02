package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/elysia-api/backend/protocol"
)

// collectProtocolGeneration is the bounded consumer used by Agent and live
// probes. It shares codecs, contract checks and stream replay with the gateway.
func (s *Server) collectProtocolGeneration(ctx context.Context, candidate gatewayCandidate, request *protocol.Request, record *usageRecord, onText func(protocol.NodeKind, string)) (*protocol.Response, error) {
	if err := protocol.IssuesError(protocol.CheckRoute(request, candidate.compiled, candidate.binding, candidate.scope, candidate.operation.Transport)); err != nil {
		return nil, err
	}
	if candidate.operation.Kind != "generate" || candidate.operation.Transport == protocol.WebSocket {
		return nil, fmt.Errorf("generation collection requires an HTTP JSON, SSE or NDJSON generate operation")
	}
	if err := s.validateOutbound(candidate.model.BaseURL); err != nil {
		return nil, err
	}
	options := protocol.EvaluationContext{Scope: candidate.scope}
	body, err := candidate.compiled.EncodeRequest(ctx, request, options)
	if err != nil {
		return nil, err
	}
	if record != nil {
		record.OutgoingBody = record.sanitizeBody(body)
	}
	response, err := s.openaiAdapter.SendProtocolRequest(ctx, candidate.model.BaseURL, candidate.model.APIKey, candidate.operation, body, map[string]string{"model": candidate.model.Name})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if record != nil {
		record.StatusCode = response.StatusCode
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &gatewayFailure{response.StatusCode, fmt.Errorf("upstream returned HTTP %d", response.StatusCode)}
	}
	if candidate.operation.Transport == protocol.HTTPJSON {
		body, err := protocol.ReadBoundedBody(response.Body, protocol.DefaultLimits().BufferBytes)
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
			updateRecordProtocolUsage(record, result.Usage)
		}
		return result, protocol.IssuesError(protocol.CheckModelResponse(result, candidate.compiled, candidate.binding, candidate.scope))
	}
	target := protocol.Target{Protocol: candidate.compiled.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: candidate.binding.Capabilities}
	collector, err := protocol.NewResponseCollector(target, protocol.DefaultLimits())
	if err != nil {
		return nil, err
	}
	err = protocol.ReadFrames(ctx, response.Body, candidate.operation, protocol.DefaultLimits().BufferBytes, func(frame protocol.Value, metadata protocol.Object) error {
		if record != nil {
			record.appendStreamEvent(string(frame.Bytes()))
		}
		options.Values = metadata
		events, err := candidate.compiled.DecodeEvents(ctx, frame, options)
		if err != nil {
			return err
		}
		for _, event := range events {
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
	if err != nil {
		return nil, err
	}
	return collector.Finish()
}
