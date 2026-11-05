package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"

	"github.com/elysia-api/backend/internal/eventstream"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

const gatewayStreamErrorTrailer = "X-Elysia-Stream-Error"

// gatewayStream owns the state for one upstream response. It never resubmits
// generation; source accounting and client projection have distinct replays.
type gatewayStream struct {
	server             *Server
	context            *app.RequestContext
	ctx                context.Context
	writer             network.ExtWriter
	record             *usageRecord
	plan               *gatewayResponsePlan
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
	deliveryFailure    error
	isFailureDelivered bool
	acceptedScratch    []protocol.Event
}

func (s *Server) forwardGatewayStream(ctx context.Context, c *app.RequestContext, record *usageRecord, plan *gatewayResponsePlan, candidate gatewayCandidate, response *http.Response) error {
	limits := plan.ingress.ResourceLimits()
	operationOptions, err := (protocol.EvaluationContext{Scope: candidate.scope}).WithOperation(record.RequestID, record.StartedAt.Unix())
	if err != nil {
		return err
	}
	options := operationOptions
	options.State, options.Diagnostics = protocol.NewEvaluationState(), &protocol.DiagnosticSink{}
	defer options.State.Release()
	options.ClientOutput = plan.clientOutput
	defer func() { record.appendConversionIssues(options.Diagnostics.Issues()) }()
	route := candidate.conversionContext(plan.ingress, true).WithOperation(operationOptions)
	route.Delivery = protocol.NewDeliveryState()
	options = options.WithConversion(candidate.conversion, route)
	eventState := protocol.NewConversionEventState(candidate.conversion, route)
	defer eventState.Release()
	var carriers *builtin.ContinuationStream
	var collector *protocol.ResponseCollector
	if candidate.continuation != nil {
		candidate.continuation.sink = options.Diagnostics
		collector, _ = protocol.NewResponseCollector(candidate.sourceEventTarget(), limits)
		defer collector.Release()
		if candidate.continuation.isPackage {
			defer candidate.continuation.binding.Release()
			options = options.WithContinuation(candidate.continuation.binding)
		} else {
			carriers = &builtin.ContinuationStream{Family: plan.ingress.Identity().Family}
		}
	}
	if options.ClientOutput == nil {
		options.ClientOutput = &protocol.ClientOutput{}
	}
	target := plan.ingress.Target(protocol.EncodeEvent, candidate.scope)
	replay, err := protocol.NewEventReplay(target, limits)
	if err != nil {
		return err
	}
	defer replay.Release()
	c.Header("Content-Type", protocol.TransportContentType(plan.operation.Transport))
	c.Header("Cache-Control", "no-cache")
	c.Header("Trailer", gatewayStreamErrorTrailer)
	wireValidation := operationOptions
	if plan.ingress.CoreVersion() != protocol.ProtocolCoreVersion {
		wireValidation.State = protocol.NewEvaluationState()
		defer wireValidation.State.Release()
	}
	finalValidation, err := plan.ingress.NewWireStreamValidation(candidate.scope)
	if err != nil {
		return err
	}
	defer finalValidation.Release()
	sourceOptions := options
	if plan.ingress.CoreVersion() == protocol.ProtocolCoreVersion {
		options = options.WithOutputStream(finalValidation)
	}
	stream := &gatewayStream{server: s, context: c, ctx: ctx, writer: startStream(c), record: record, plan: plan, candidate: candidate,
		limits: limits, options: options, route: route, eventState: eventState, carriers: carriers,
		collector: collector, replay: replay,
		wireValidation: wireValidation, finalValidation: finalValidation}
	stream.source, err = newProtocolSourceStream(ctx, candidate, plan.ingress, route, sourceOptions, record, stream.consumeSourceFrame)
	if err != nil {
		return err
	}
	if err := stream.bindBudget(); err != nil {
		stream.source.release()
		return err
	}
	err = protocol.ReadFrames(ctx, response.Body, candidate.operation, candidate.compiled.ResourceLimits().BufferBytes, stream.source.Push)
	return stream.finish(err)
}

func (stream *gatewayStream) bindBudget() error {
	if stream.plan.ingress.CoreVersion() != protocol.ProtocolCoreVersion || stream.candidate.compiled.CoreVersion() != protocol.ProtocolCoreVersion {
		return nil
	}
	budget := protocol.NewStreamBudget(stream.limits)
	if err := budget.Bind(stream.options.State, stream.source.wireValidation, stream.source.replay, stream.finalValidation, stream.replay, stream.eventState); err != nil {
		return err
	}
	if stream.collector != nil {
		return budget.Bind(stream.collector, stream.candidate.continuation.binding)
	}
	return nil
}

func (stream *gatewayStream) emit(value protocol.Value) error {
	if stream.deliveryFailure != nil {
		return stream.deliveryFailure
	}
	if stream.record.FirstByteMs == 0 {
		stream.record.FirstByteMs = time.Since(stream.record.StartedAt).Milliseconds()
	}
	before := value
	if stream.candidate.conversion.HasPhase(protocol.ConversionWire) {
		converted, e := stream.candidate.conversion.ApplyValue(stream.ctx, protocol.ConversionWire, value, stream.route, stream.options.Diagnostics)
		if e != nil {
			return e
		}
		if stream.plan.ingress.CoreVersion() != protocol.ProtocolCoreVersion {
			if _, e = stream.plan.ingress.DecodeFrame(stream.ctx, converted, stream.wireValidation); e != nil {
				return e
			}
		}
		value = converted
	}
	var single [1]protocol.Value
	single[0] = value
	frames := single[:]
	var originals []protocol.Value
	continuation := stream.candidate.continuation
	if continuation != nil && continuation.isPackage {
		prior, inserted, err := stream.plan.ingress.EncodeContinuationTransformedFrames(stream.ctx, before, value, stream.options)
		if err != nil {
			return err
		}
		if inserted != nil {
			frames = inserted
			originals = prior
		}
	} else if continuation != nil {
		stream.carriers.Tokens = stream.candidate.continuation.tokens
		var err error
		frames, err = stream.carriers.Frames(value)
		if err != nil {
			return err
		}
	}
	for index, frame := range frames {
		var err error
		if stream.plan.ingress.CoreVersion() == protocol.ProtocolCoreVersion {
			original := before
			if originals != nil {
				original = originals[index]
			}
			err = stream.finalValidation.ConsumeTransformed(stream.ctx, original, frame, stream.options)
		} else {
			err = stream.finalValidation.Consume(stream.ctx, frame)
		}
		if err != nil {
			return err
		}
		if frame.Len() > stream.limits.BufferBytes {
			return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/frame", Reason: "frame including continuation carriers exceeds target buffer limit"}})
		}
		if err := protocol.WriteFrame(stream.writer, stream.plan.operation, frame); err != nil {
			return stream.abortDelivery(err, deliveryWrite)
		}
	}
	if len(frames) == 0 {
		return nil
	}
	if err := stream.writer.Flush(); err != nil {
		return stream.abortDelivery(err, deliveryFlush)
	}
	if stream.plan.ingress.CoreVersion() == protocol.ProtocolCoreVersion {
		if err := stream.finalValidation.CommitDelivery(); err != nil {
			return err
		}
		if continuation != nil && continuation.isPackage {
			return continuation.binding.CommitDelivery()
		}
	}
	return nil
}

func (stream *gatewayStream) abortDelivery(err error, phase string) error {
	stream.deliveryFailure = wrapDownstreamError(err, phase)
	stream.finalValidation.AbortDelivery()
	if continuation := stream.candidate.continuation; continuation != nil && continuation.isPackage {
		continuation.binding.AbortDelivery()
	}
	return stream.deliveryFailure
}

func (stream *gatewayStream) consumeSourceFrame(decoded *protocol.EventFrame, metadata protocol.Object) error {
	stream.options.Values = metadata
	var err error
	stream.route, err = stream.candidate.conversion.WithFrameUsage(stream.route, decoded.Events)
	if err != nil {
		return err
	}
	acceptedEvents := stream.acceptedScratch[:0]
	if acceptedEvents == nil {
		acceptedEvents = []protocol.Event{}
	}
	defer func() {
		// No frame payload may survive in reusable slots, including on failure.
		clear(acceptedEvents)
		if cap(acceptedEvents) <= 16 {
			stream.acceptedScratch = acceptedEvents[:0]
		} else {
			stream.acceptedScratch = nil
		}
	}()
	deliveryEvents := decoded.Events
	if stream.candidate.conversion.HasAnthropicEnvelope(protocol.ConversionEvent, stream.route) {
		deliveryEvents = protocol.DeliveryFrameEvents(decoded.Events)
	}
	for eventIndex, event := range decoded.Events {
		if stream.collector != nil {
			if _, _, captureErr := stream.collector.Consume(event); captureErr != nil {
				if stream.candidate.continuation.isPackage {
					return captureErr
				}
				if err := stream.candidate.continuation.warning("continuation stream collection unavailable: " + captureErr.Error()); err != nil {
					return err
				}
				stream.collector = nil
			} else {
				if node, ordinal, ok, err := stream.collector.CompletedNode(event); err != nil {
					return err
				} else if ok {
					if err := stream.server.captureContinuationNode(stream.ctx, stream.candidate.continuation, node, ordinal); err != nil {
						return err
					}
				}
				if event.Type == protocol.ResponseFinished {
					collected, e := stream.collector.Finish()
					if e != nil {
						return e
					}
					if e = stream.server.captureContinuationResponse(stream.ctx, stream.candidate.continuation, collected); e != nil {
						return e
					}
				}
			}
		}
		projected := deliveryEvents[eventIndex]
		if stream.collector != nil && stream.candidate.continuation.isPackage {
			projected, err = stream.collector.BindContinuationEvent(projected, stream.candidate.continuation.binding)
			if err != nil {
				return err
			}
		}
		queued, e := stream.eventState.PushContext(stream.ctx, projected)
		if e != nil {
			return e
		}
		for _, next := range queued {
			if stream.candidate.conversion != nil {
				stream.route.Recoverable = stream.candidate.conversionContext(stream.plan.ingress, true).Recoverable
				next, err = stream.candidate.conversion.Event(stream.ctx, next, stream.route, stream.options.Diagnostics)
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
	if stream.eventState.Buffered {
		decoded = &protocol.EventFrame{Events: acceptedEvents}
	}
	frames, err := stream.plan.ingress.EncodeConvertedFrame(stream.ctx, decoded, acceptedEvents, stream.options)
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
		if err == nil {
			stream.route, err = stream.candidate.conversion.WithFrameUsage(stream.route, tail)
		}
		for _, event := range tail {
			if err != nil {
				break
			}
			stream.route.Recoverable = stream.candidate.conversionContext(stream.plan.ingress, true).Recoverable
			event, err = stream.candidate.conversion.Event(stream.ctx, event, stream.route, stream.options.Diagnostics)
			if err != nil {
				break
			}
			if _, err = stream.replay.Consume(event); err != nil {
				break
			}
			var frames []protocol.Value
			frames, err = stream.plan.ingress.EncodeFrame(stream.ctx, &protocol.EventFrame{Events: []protocol.Event{event}}, stream.options)
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
		frames, err = stream.plan.ingress.FinishEvents(stream.ctx, stream.options)
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
	if err == nil && stream.candidate.continuation != nil && stream.candidate.continuation.isPackage {
		err = stream.candidate.continuation.binding.Finish()
	}
	if err != nil {
		err = errors.Join(stream.terminalFailure, err)
		var deliveryFailure *downstreamWriteError
		if stream.ctx.Err() != nil || errors.As(err, &deliveryFailure) {
			// The delivery owner will record this failure. A closed/cancelled
			// stream cannot consume another projected terminal event.
			return err
		}
		if stream.isFailureDelivered {
			// A provider failure already ended the client's event lifecycle.
			// Reject an invalid tail without replacing the original cause or
			// attempting a second terminal. Both errors remain inspectable.
			stream.context.Response.Header.Trailer().Set(gatewayStreamErrorTrailer, "protocol_stream_error")
			return err
		}
		if writeErr := emitFailureEvent(stream.ctx, stream.context, stream.plan, stream.options, stream.emit, err); writeErr != nil {
			return writeErr
		}
		return err
	}
	if stream.terminalFailure != nil {
		// The provider's error has already been delivered and validated. Keep
		// usage tails, record a failed call, and never append a second error or
		// a synthetic success marker to that native/projected terminal.
		stream.context.Response.Header.Trailer().Set(gatewayStreamErrorTrailer, "protocol_stream_error")
		return stream.terminalFailure
	}
	if stream.plan.operation.Framing != nil && len(stream.plan.operation.Framing.Done) > 0 {
		// Done lists accepted alternatives; output uses the first canonical marker.
		marker := stream.plan.operation.Framing.Done[0]
		if stream.plan.operation.Transport == protocol.SSE {
			if err := eventstream.Write(stream.writer, "", marker); err != nil {
				return stream.abortDelivery(err, deliveryWrite)
			}
		} else if _, err := fmt.Fprintln(stream.writer, marker); err != nil {
			return stream.abortDelivery(err, deliveryWrite)
		}
	}
	if err := stream.writer.Flush(); err != nil {
		return stream.abortDelivery(err, deliveryFlush)
	}
	return nil
}

// emitFailureEvent surfaces a late mapping failure after the response headers
// are already committed. Never replay a generation once any downstream frame is
// written; the trailer is the signal when the target has no error event of its
// own. A failure to deliver the error frame is reported back to the caller so it
// can be logged, but the original stream error is what the caller still returns.
func emitFailureEvent(ctx context.Context, c *app.RequestContext, plan *gatewayResponsePlan, options protocol.EvaluationContext, emit func(protocol.Value) error, cause error) error {
	if !responseCommitted(c) {
		return nil
	}
	c.Response.Header.Trailer().Set(gatewayStreamErrorTrailer, "protocol_stream_error")
	errorValue, encodeErr := protocol.EncodeValue(map[string]string{"category": "upstream", "message": cause.Error()})
	if encodeErr != nil {
		return fmt.Errorf("%w; downstream error frame: %v", cause, encodeErr)
	}
	failure := protocol.Event{SchemaVersion: plan.ingress.CoreVersion(), Type: protocol.OperationFailed, Error: errorValue}
	frames, encodeErr := plan.ingress.EncodeFrames(ctx, failure, options)
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
