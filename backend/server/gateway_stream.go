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
)

const gatewayStreamErrorTrailer = "X-Elysia-Stream-Error"

// gatewayStream owns the state for one upstream response. It never resubmits
// generation; source accounting and client projection have distinct replays.
type gatewayStream struct {
	server             *Server
	context            *app.RequestContext
	ctx                context.Context
	writer             network.ExtWriter
	writeFrame         func(protocol.Value) error
	record             *usageRecord
	plan               *gatewayResponsePlan
	candidate          gatewayCandidate
	limits             protocol.Limits
	options            protocol.EvaluationContext
	route              protocol.ConversionContext
	eventState         *protocol.ConversionEventState
	source             *protocolSourceStream
	collector          *protocol.ResponseCollector
	replay             *protocol.EventReplay
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
	var collector *protocol.ResponseCollector
	if candidate.continuation != nil {
		candidate.continuation.sink = options.Diagnostics
		collector, _ = protocol.NewResponseCollector(candidate.sourceEventTarget(), limits)
		defer collector.Release()
		defer candidate.continuation.binding.Release()
		options = options.WithContinuation(candidate.continuation.binding)
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
	finalValidation, err := plan.ingress.NewWireStreamValidation(candidate.scope)
	if err != nil {
		return err
	}
	defer finalValidation.Release()
	sourceOptions := options
	options = options.WithOutputStream(finalValidation)
	stream := &gatewayStream{server: s, context: c, ctx: ctx, writer: startStream(c), record: record, plan: plan, candidate: candidate,
		limits: limits, options: options, route: route, eventState: eventState,
		collector: collector, replay: replay,
		finalValidation: finalValidation}
	stream.source, err = newProtocolSourceStream(ctx, candidate, route, sourceOptions, record, stream.consumeSourceFrame)
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
	budget := protocol.NewStreamBudget(stream.limits)
	if err := budget.Bind(stream.options.State, stream.source.state, stream.source.wireValidation, stream.source.replay, stream.finalValidation, stream.replay, stream.eventState); err != nil {
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
	if stream.writeFrame == nil {
		var err error
		stream.writeFrame, err = protocol.NewFrameWriter(stream.writer, stream.plan.operation)
		if err != nil {
			return err
		}
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
		value = converted
	}
	var single [1]protocol.Value
	single[0] = value
	frames := single[:]
	var originals []protocol.Value
	continuation := stream.candidate.continuation
	if continuation != nil {
		prior, inserted, err := stream.plan.ingress.EncodeContinuationTransformedFrames(stream.ctx, before, value, stream.options)
		if err != nil {
			return err
		}
		if inserted != nil {
			frames = inserted
			originals = prior
		}
	}
	for index, frame := range frames {
		original := before
		if originals != nil {
			original = originals[index]
		}
		if err := stream.finalValidation.ConsumeTransformed(stream.ctx, original, frame, stream.options); err != nil {
			return err
		}
		if frame.Len() > stream.limits.BufferBytes {
			return protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.LimitExceeded, Severity: protocol.SeverityError, Stage: "continuation", Path: "/frame", Reason: "frame including continuation carriers exceeds target buffer limit"}})
		}
		if err := stream.writeFrame(frame); err != nil {
			return stream.abortDelivery(err, deliveryWrite)
		}
	}
	if len(frames) == 0 {
		return nil
	}
	if err := stream.writer.Flush(); err != nil {
		return stream.abortDelivery(err, deliveryFlush)
	}
	if err := stream.finalValidation.CommitDelivery(); err != nil {
		return err
	}
	if continuation != nil {
		return continuation.binding.CommitDelivery()
	}
	return nil
}

func (stream *gatewayStream) abortDelivery(err error, phase string) error {
	stream.deliveryFailure = wrapDownstreamError(err, phase)
	stream.finalValidation.AbortDelivery()
	if continuation := stream.candidate.continuation; continuation != nil {
		continuation.binding.AbortDelivery()
	}
	return stream.deliveryFailure
}

func (stream *gatewayStream) captureContinuation(event protocol.Event) error {
	if stream.collector == nil {
		return nil
	}
	if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
		// Failed generations cannot authenticate unfinished nodes as recoverable history.
		stream.collector.Release()
		stream.collector = nil
		return nil
	}
	if _, _, err := stream.collector.Consume(event); err != nil {
		return err
	}
	node, ordinal, isComplete, err := stream.collector.CompletedNode(event)
	if err != nil {
		return err
	}
	if isComplete {
		if err := stream.server.captureContinuationNode(stream.ctx, stream.candidate.continuation, node, ordinal); err != nil {
			return err
		}
	}
	if event.Type != protocol.ResponseFinished {
		return nil
	}
	response, err := stream.collector.Finish()
	if err != nil {
		return err
	}
	return stream.server.captureContinuationResponse(stream.ctx, stream.candidate.continuation, response)
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
	for index, event := range decoded.Events {
		if err := stream.captureContinuation(event); err != nil {
			return err
		}
		projected := event
		if stream.collector != nil {
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
				next, err = stream.candidate.conversion.Event(stream.ctx, next, stream.route, stream.options.Diagnostics)
				if err != nil {
					return err
				}
			}
			// One wire digest identifies one projected event; expansions keep semantic dedup.
			replayFrame := decoded
			if stream.eventState.Buffered || len(queued) != 1 {
				replayFrame = nil
			}
			accepted, e := replayFrame.ConsumeEvent(stream.replay, index, next)
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
		err = stream.eventState.Finish()
	}
	if err == nil {
		err = stream.replay.Finish()
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
	if err == nil && stream.candidate.continuation != nil {
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
