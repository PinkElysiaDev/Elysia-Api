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

// gatewayStream owns the state for one upstream response. It never resubmits
// generation; source accounting and client projection have distinct replays.
type gatewayStream struct {
	server             *Server
	context            *gin.Context
	record             *usageRecord
	plan               *gatewayPlan
	candidate          gatewayCandidate
	limits             protocol.Limits
	options            protocol.EvaluationContext
	route              protocol.ConversionContext
	eventState         *protocol.ConversionEventState
	source             *protocolSourceStream
	carriers           *builtin.ContinuationStream
	collector          *protocol.ResponseCollector
	replay             *protocol.EventReplay
	wireValidation     protocol.EvaluationContext
	finalValidation    *protocol.WireStreamValidation
	terminalFailure    error
	isFailureDelivered bool
}

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
	stream := &gatewayStream{server: s, context: c, record: record, plan: plan, candidate: candidate,
		limits: limits, options: options, route: route, eventState: eventState, carriers: carriers,
		collector: collector, replay: replay,
		wireValidation: wireValidation, finalValidation: finalValidation}
	stream.source, err = newProtocolSourceStream(c.Request.Context(), candidate, plan.ingress, route, options, record, stream.consumeSourceFrame)
	if err != nil {
		return err
	}
	err = protocol.ReadFrames(c.Request.Context(), response.Body, candidate.operation, candidate.compiled.ResourceLimits().BufferBytes, stream.source.Push)
	return stream.finish(err)
}

func (stream *gatewayStream) emit(value protocol.Value) error {
	if stream.record.FirstByteMs == 0 {
		stream.record.FirstByteMs = time.Since(stream.record.StartedAt).Milliseconds()
	}
	if stream.candidate.conversion.HasPhase(protocol.ConversionWire) {
		converted, e := stream.candidate.conversion.ApplyValue(stream.context.Request.Context(), protocol.ConversionWire, value, stream.route, stream.options.Diagnostics)
		if e != nil {
			return e
		}
		if _, e = stream.plan.ingress.DecodeFrame(stream.context.Request.Context(), converted, stream.wireValidation); e != nil {
			return e
		}
		value = converted
	}
	frames := []protocol.Value{value}
	if stream.candidate.continuation != nil {
		stream.carriers.Tokens = stream.candidate.continuation.tokens
		var err error
		frames, err = stream.carriers.Frames(value)
		if err != nil {
			return err
		}
	}
	for _, frame := range frames {
		if err := stream.finalValidation.Consume(stream.context.Request.Context(), frame); err != nil {
			return err
		}
		if len(frame.Bytes()) > stream.limits.BufferBytes {
			return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/frame", Reason: "frame including continuation carriers exceeds target buffer limit"}})
		}
		if err := protocol.WriteFrame(stream.context.Writer, stream.plan.operation, frame); err != nil {
			return err
		}
	}
	stream.context.Writer.Flush()
	return nil
}

func (stream *gatewayStream) consumeSourceFrame(decoded *protocol.EventFrame, metadata protocol.Object) error {
	stream.options.Values = metadata
	var err error
	originalEvents, _ := protocol.EncodeValue(decoded.Events)
	acceptedEvents := []protocol.Event{}
	deliveryEvents := decoded.Events
	if stream.candidate.conversion.HasAnthropicEnvelope(protocol.ConversionEvent, stream.route) {
		deliveryEvents = protocol.DeliveryFrameEvents(decoded.Events)
	}
	for eventIndex, event := range decoded.Events {
		if stream.collector != nil {
			if _, _, captureErr := stream.collector.Consume(event); captureErr != nil {
				if err := stream.candidate.continuation.warning("continuation stream collection unavailable: " + captureErr.Error()); err != nil {
					return err
				}
				stream.collector = nil
			} else {
				if node, ordinal, ok := stream.collector.CompletedNode(event); ok {
					if err := stream.server.captureContinuationNode(stream.context, stream.candidate.continuation, node, ordinal); err != nil {
						return err
					}
				}
				if event.Type == protocol.ResponseFinished {
					collected, e := stream.collector.Finish()
					if e != nil {
						return e
					}
					if e = stream.server.captureContinuationResponse(stream.context, stream.candidate.continuation, collected); e != nil {
						return e
					}
				}
			}
		}
		queued, e := stream.eventState.Push(deliveryEvents[eventIndex])
		if e != nil {
			return e
		}
		for _, next := range queued {
			if stream.candidate.conversion != nil {
				stream.route.Recoverable = stream.candidate.conversionContext(stream.plan.ingress, true).Recoverable
				next, err = stream.candidate.conversion.Event(stream.context.Request.Context(), next, stream.route, stream.options.Diagnostics)
				if err != nil {
					return err
				}
			}
			accepted, e := stream.replay.Consume(next)
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
	if stream.eventState.Buffered || !bytes.Equal(originalEvents.Bytes(), updatedEvents.Bytes()) {
		decoded = &protocol.EventFrame{Events: acceptedEvents}
	} else {
		decoded.Events = acceptedEvents
	}
	frames, err := stream.plan.ingress.EncodeFrame(stream.context.Request.Context(), decoded, stream.options)
	if err != nil {
		return err
	}
	for _, wire := range frames {
		if err := stream.emit(wire); err != nil {
			return err
		}
	}
	if len(frames) > 0 {
		for _, event := range acceptedEvents {
			if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
				stream.isFailureDelivered = true
			}
		}
	}
	return nil
}

func (stream *gatewayStream) finish(err error) error {
	err = stream.source.Finish(err)
	stream.terminalFailure = stream.source.terminalFailure
	if err == nil {
		var tail []protocol.Event
		tail, err = stream.eventState.Drain()
		for _, event := range tail {
			if err != nil {
				break
			}
			stream.route.Recoverable = stream.candidate.conversionContext(stream.plan.ingress, true).Recoverable
			event, err = stream.candidate.conversion.Event(stream.context.Request.Context(), event, stream.route, stream.options.Diagnostics)
			if err != nil {
				break
			}
			if _, err = stream.replay.Consume(event); err != nil {
				break
			}
			var frames []protocol.Value
			frames, err = stream.plan.ingress.EncodeFrame(stream.context.Request.Context(), &protocol.EventFrame{Events: []protocol.Event{event}}, stream.options)
			if err != nil {
				break
			}
			for _, frame := range frames {
				if err = stream.emit(frame); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = stream.replay.Finish()
		}
	}
	if err == nil {
		var frames []protocol.Value
		frames, err = stream.plan.ingress.FinishEvents(stream.context.Request.Context(), stream.options)
		if err == nil {
			for _, frame := range frames {
				if writeErr := stream.emit(frame); writeErr != nil {
					err = writeErr
					break
				}
			}
		}
	}
	if err == nil {
		err = stream.finalValidation.Finish()
	}
	if err != nil {
		err = errors.Join(stream.terminalFailure, err)
		if stream.isFailureDelivered {
			// A provider failure already ended the client's event lifecycle.
			// Reject an invalid tail without replacing the original cause or
			// attempting a second terminal. Both errors remain inspectable.
			stream.context.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
			return err
		}
		if writeErr := emitFailureEvent(stream.context, stream.plan, stream.options, stream.emit, err); writeErr != nil {
			return writeErr
		}
		return err
	}
	if stream.terminalFailure != nil {
		// The provider's error has already been delivered and validated. Keep
		// usage tails, record a failed call, and never append a second error or
		// a synthetic success marker to that native/projected terminal.
		stream.context.Header(gatewayStreamErrorTrailer, "protocol_stream_error")
		return stream.terminalFailure
	}
	if stream.plan.operation.Framing != nil && len(stream.plan.operation.Framing.Done) > 0 {
		// Done lists accepted alternatives; output uses the first canonical marker.
		marker := stream.plan.operation.Framing.Done[0]
		if stream.plan.operation.Transport == protocol.SSE {
			if _, err := fmt.Fprintf(stream.context.Writer, "data: %s\n\n", marker); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(stream.context.Writer, marker); err != nil {
			return err
		}
	}
	stream.context.Writer.Flush()
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
