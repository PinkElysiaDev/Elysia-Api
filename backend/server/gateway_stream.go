package server

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/gin-gonic/gin"
)

const gatewayStreamErrorTrailer = "X-Elysia-Stream-Error"

func (s *Server) forwardGatewayStream(c *gin.Context, record *usageRecord, plan *gatewayPlan, candidate gatewayCandidate, response *http.Response) error {
	limits := plan.ingress.ResourceLimits()
	options := protocol.EvaluationContext{Scope: candidate.scope, State: protocol.NewEvaluationState(), Diagnostics: &protocol.DiagnosticSink{}}
	options.ClientOutput = plan.request.ClientOutput
	if candidate.prepared != nil {
		options.ClientOutput = candidate.prepared.ClientOutput
	}
	defer func() { record.appendConversionIssues(options.Diagnostics.Issues()) }()
	route := candidate.conversionContext(plan.ingress, true)
	route.Delivery = protocol.NewDeliveryState()
	eventState := protocol.NewConversionEventState(candidate.conversion, route)
	carriers := &builtin.ContinuationStream{Family: plan.ingress.Identity().Family}
	var collector *protocol.ResponseCollector
	if candidate.continuation != nil {
		candidate.continuation.sink = options.Diagnostics
		collector, _ = protocol.NewResponseCollector(protocol.Target{Protocol: candidate.compiled.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: candidate.binding.Capabilities}, limits)
	}
	if options.ClientOutput == nil {
		options.ClientOutput = &protocol.ClientOutput{}
	}
	sourceReplay, err := protocol.NewEventReplay(protocol.Target{Protocol: candidate.compiled.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: candidate.binding.Capabilities}, candidate.compiled.ResourceLimits())
	if err != nil {
		return err
	}
	target := protocol.Target{Protocol: plan.ingress.Identity(), Direction: protocol.EncodeEvent, Scope: candidate.scope, Capabilities: plan.ingress.Capabilities(protocol.EncodeEvent)}
	replay, err := protocol.NewEventReplay(target, limits)
	if err != nil {
		return err
	}
	c.Header("Content-Type", protocol.TransportContentType(plan.operation.Transport))
	c.Header("Cache-Control", "no-cache")
	c.Header("Trailer", gatewayStreamErrorTrailer)
	wireValidation := protocol.EvaluationContext{Scope: candidate.scope, State: protocol.NewEvaluationState()}
	finalValidation, err := plan.ingress.NewWireStreamValidation(candidate.scope)
	if err != nil {
		return err
	}
	var terminalFailure error
	failureDelivered := false
	emit := func(value protocol.Value) error {
		if record.FirstByteMs == 0 {
			record.FirstByteMs = time.Since(record.StartedAt).Milliseconds()
		}
		if candidate.conversion.HasPhase(protocol.ConversionWire) {
			converted, e := candidate.conversion.ApplyValue(c.Request.Context(), protocol.ConversionWire, value, route, options.Diagnostics)
			if e != nil {
				return e
			}
			if _, e = plan.ingress.DecodeFrame(c.Request.Context(), converted, wireValidation); e != nil {
				return e
			}
			value = converted
		}
		frames := []protocol.Value{value}
		if candidate.continuation != nil {
			carriers.Tokens = candidate.continuation.tokens
			var err error
			frames, err = carriers.Frames(value)
			if err != nil {
				return err
			}
		}
		for _, frame := range frames {
			if err := finalValidation.Consume(c.Request.Context(), frame); err != nil {
				return err
			}
			if len(frame.Bytes()) > limits.BufferBytes {
				return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/frame", Reason: "frame including continuation carriers exceeds target buffer limit"}})
			}
			if err := protocol.WriteFrame(c.Writer, plan.operation, frame); err != nil {
				return err
			}
		}
		c.Writer.Flush()
		return nil
	}
	err = protocol.ReadFrames(c.Request.Context(), response.Body, candidate.operation, candidate.compiled.ResourceLimits().BufferBytes, func(frame protocol.Value, metadata protocol.Object) error {
		record.appendStreamEvent(string(frame.Bytes()))
		options.Values = metadata
		decoded, err := candidate.compiled.DecodeFrame(c.Request.Context(), frame, options)
		if err != nil {
			return err
		}
		if err := observeHostedTools(record, candidate.compiled, frame.Bytes()); err != nil {
			return err
		}
		// Validate the whole provider frame before using same-frame usage in a client start.
		for _, event := range decoded.Events {
			if event.Response != nil {
				// Two layers, different consumers: CheckGenerationOutcome rejects a
				// terminal response whose status/error disagree (the streaming
				// equivalent of an HTTP failure); CheckModelEvent below applies the
				// declared model contract and upstream capabilities to every event.
				if err := protocol.CheckGenerationOutcome(event.Response); err != nil {
					return err
				}
			}
			if err := protocol.IssuesError(protocol.CheckModelEvent(event, candidate.compiled, candidate.binding, candidate.scope)); err != nil {
				return err
			}
		}
		originalEvents, _ := protocol.EncodeValue(decoded.Events)
		acceptedEvents := []protocol.Event{}
		deliveryEvents := decoded.Events
		if candidate.conversion.HasAnthropicEnvelope(protocol.ConversionEvent, route) {
			deliveryEvents = protocol.DeliveryFrameEvents(decoded.Events)
		}
		for eventIndex, event := range decoded.Events {
			if _, err := sourceReplay.Consume(event); err != nil {
				return err
			}
			if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
				status := "failed"
				if event.Type == protocol.OperationCancelled {
					status = "cancelled"
				}
				terminalFailure = &protocol.GenerationFailure{Payload: event.Error, Status: protocol.StringValue(status)}
			}
			updateRecordProtocolUsage(record, sourceReplay.Usage())
			if collector != nil {
				if _, _, captureErr := collector.Consume(event); captureErr != nil {
					if err := candidate.continuation.warning("continuation stream collection unavailable: " + captureErr.Error()); err != nil {
						return err
					}
					collector = nil
				} else {
					if node, ordinal, ok := collector.CompletedNode(event); ok {
						if err := s.captureContinuationNode(c, candidate.continuation, node, ordinal); err != nil {
							return err
						}
					}
					if event.Type == protocol.ResponseFinished {
						collected, e := collector.Finish()
						if e != nil {
							return e
						}
						if e = s.captureContinuationResponse(c, candidate.continuation, collected); e != nil {
							return e
						}
					}
				}
			}
			queued, e := eventState.Push(deliveryEvents[eventIndex])
			if e != nil {
				return e
			}
			for _, next := range queued {
				if candidate.conversion != nil {
					route.Recoverable = candidate.conversionContext(plan.ingress, true).Recoverable
					next, err = candidate.conversion.Event(c.Request.Context(), next, route, options.Diagnostics)
					if err != nil {
						return err
					}
				}
				accepted, e := replay.Consume(next)
				if e != nil {
					return e
				}
				if accepted {
					acceptedEvents = append(acceptedEvents, next)
				}
			}
		}
		if len(decoded.Events) > 0 && len(acceptedEvents) == 0 {
			return nil
		}
		updatedEvents, _ := protocol.EncodeValue(acceptedEvents)
		if eventState.Buffered || !bytes.Equal(originalEvents.Bytes(), updatedEvents.Bytes()) {
			decoded = &protocol.EventFrame{Events: acceptedEvents}
		} else {
			decoded.Events = acceptedEvents
		}
		frames, err := plan.ingress.EncodeFrame(c.Request.Context(), decoded, options)
		if err != nil {
			return err
		}
		for _, wire := range frames {
			if err := emit(wire); err != nil {
				return err
			}
		}
		if len(frames) > 0 {
			for _, event := range acceptedEvents {
				if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
					failureDelivered = true
				}
			}
		}
		return nil
	})
	if err == nil {
		var tail []protocol.Event
		tail, err = eventState.Drain()
		for _, event := range tail {
			if err != nil {
				break
			}
			route.Recoverable = candidate.conversionContext(plan.ingress, true).Recoverable
			event, err = candidate.conversion.Event(c.Request.Context(), event, route, options.Diagnostics)
			if err != nil {
				break
			}
			if _, err = replay.Consume(event); err != nil {
				break
			}
			var frames []protocol.Value
			frames, err = plan.ingress.EncodeFrame(c.Request.Context(), &protocol.EventFrame{Events: []protocol.Event{event}}, options)
			if err != nil {
				break
			}
			for _, frame := range frames {
				if err = emit(frame); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = sourceReplay.Finish()
		}
		if err == nil {
			err = replay.Finish()
		}
	}
	if err == nil {
		var frames []protocol.Value
		frames, err = plan.ingress.FinishEvents(c.Request.Context(), options)
		if err == nil {
			for _, frame := range frames {
				if writeErr := emit(frame); writeErr != nil {
					err = writeErr
					break
				}
			}
		}
	}
	if err == nil {
		err = finalValidation.Finish()
	}
	if err != nil {
		if failureDelivered {
			// A provider failure already ended the client's event lifecycle.
			// Reject an invalid tail without replacing the original cause or
			// attempting a second terminal. Both errors remain inspectable.
			c.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
			return errors.Join(terminalFailure, err)
		}
		if writeErr := emitFailureEvent(c, plan, options, emit, err); writeErr != nil {
			return writeErr
		}
		return err
	}
	if terminalFailure != nil {
		// The provider's error has already been delivered and validated. Keep
		// usage tails, record a failed call, and never append a second error or
		// a synthetic success marker to that native/projected terminal.
		c.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
		return terminalFailure
	}
	if plan.operation.Framing != nil {
		for _, marker := range plan.operation.Framing.Done {
			if plan.operation.Transport == protocol.SSE {
				if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", marker); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(c.Writer, marker); err != nil {
				return err
			}
			// Done lists accepted alternatives; output uses the first canonical marker.
			break
		}
	}
	c.Writer.Flush()
	return nil
}

// emitFailureEvent surfaces a late mapping failure after the response headers
// are already committed. Never replay a generation once any downstream frame is
// written; the trailer is the signal when the target has no error event of its
// own. A failure to deliver the error frame is reported back to the caller so it
// can be logged, but the original stream error is what the caller still returns.
func emitFailureEvent(c *gin.Context, plan *gatewayPlan, options protocol.EvaluationContext, emit func(protocol.Value) error, cause error) error {
	if !c.Writer.Written() {
		return nil
	}
	c.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
	errorValue, encodeErr := protocol.EncodeValue(map[string]string{"category": "upstream", "message": cause.Error()})
	if encodeErr != nil {
		return fmt.Errorf("%w; downstream error frame: %v", cause, encodeErr)
	}
	failure := protocol.Event{SchemaVersion: protocol.SemanticSchemaVersion, Type: protocol.OperationFailed, Error: errorValue}
	frames, encodeErr := plan.ingress.EncodeFrames(c.Request.Context(), failure, options)
	if encodeErr != nil {
		return fmt.Errorf("%w; downstream error frame: %v", cause, encodeErr)
	}
	for _, frame := range frames {
		if writeErr := emit(frame); writeErr != nil {
			return fmt.Errorf("%w; downstream error frame: %v", cause, writeErr)
		}
	}
	return nil
}
