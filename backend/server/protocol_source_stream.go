package server

import (
	"context"

	"github.com/elysia-api/backend/protocol"
)

// protocolSourceStream owns upstream normalization, validation and accounting.
// Consumers receive validated frames; client projection never changes raw usage.
type protocolSourceStream struct {
	ctx             context.Context
	candidate       gatewayCandidate
	record          *usageRecord
	options         protocol.EvaluationContext
	state           *protocol.SourceEventState
	wireValidation  *protocol.WireStreamValidation
	replay          *protocol.EventReplay
	consume         func(*protocol.EventFrame, protocol.Object) error
	terminalFailure error
}

func (candidate gatewayCandidate) sourceEventTarget() protocol.Target {
	target := candidate.compiled.TargetWithCapabilities(protocol.DecodeEvent, candidate.scope, candidate.binding.Capabilities)
	target.Direction = protocol.EncodeEvent
	return target
}

func newProtocolSourceStream(ctx context.Context, candidate gatewayCandidate, target *protocol.Compiled, route protocol.ConversionContext, options protocol.EvaluationContext, record *usageRecord, consume func(*protocol.EventFrame, protocol.Object) error) (*protocolSourceStream, error) {
	var wireValidation *protocol.WireStreamValidation
	if candidate.compiled.CoreVersion() == protocol.ProtocolCoreVersion {
		var err error
		wireValidation, err = candidate.compiled.NewWireInputStreamValidation(candidate.scope)
		if err != nil {
			return nil, err
		}
	}
	replay, err := protocol.NewEventReplay(candidate.sourceEventTarget(), candidate.compiled.ResourceLimits())
	if err != nil {
		return nil, err
	}
	return &protocolSourceStream{ctx: ctx, candidate: candidate, record: record, options: options, consume: consume, replay: replay, wireValidation: wireValidation, state: protocol.NewSourceEventState(candidate.conversion, route, candidate.compiled, target, options.Diagnostics)}, nil
}

func (source *protocolSourceStream) Push(value protocol.Value, metadata protocol.Object) error {
	if source.record != nil && source.record.bodyOpts.maxBytes > 0 {
		source.record.appendStreamEvent(value.JSONString())
	}
	frames, err := source.state.Push(source.ctx, value, metadata)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		if err := source.decode(frame); err != nil {
			return err
		}
	}
	return nil
}

func (source *protocolSourceStream) decode(frame protocol.SourceFrame) error {
	source.options.Values = frame.Metadata
	var decoded *protocol.EventFrame
	var err error
	if source.wireValidation != nil {
		decoded, err = source.wireValidation.DecodeFrame(source.ctx, frame.Value, source.options)
	} else {
		decoded, err = source.candidate.compiled.DecodeFrame(source.ctx, frame.Value, source.options)
	}
	if err != nil {
		return err
	}
	if source.record != nil {
		if err := observeHostedTools(source.record, source.candidate.compiled, frame.Value.Bytes()); err != nil {
			return err
		}
	}
	for _, event := range decoded.Events {
		if event.Type == protocol.OperationFailed || event.Type == protocol.OperationCancelled {
			status := "failed"
			if event.Type == protocol.OperationCancelled {
				status = "cancelled"
			}
			source.terminalFailure = &protocol.GenerationFailure{Payload: event.Error, Status: protocol.StringValue(status)}
		}
		if event.Response != nil {
			if err := protocol.CheckGenerationOutcome(event.Response); err != nil {
				source.terminalFailure = err
				return err
			}
		}
		if err := protocol.IssuesError(protocol.CheckModelEvent(event, source.candidate.compiled, source.candidate.binding, source.candidate.scope)); err != nil {
			return err
		}
	}
	for _, event := range decoded.Events {
		isAccepted, err := source.replay.Consume(event)
		if err != nil {
			return source.candidate.compiled.AnnotateSourceEventError(err)
		}
		hasUsageUpdate := event.Usage != nil || event.Response != nil && event.Response.Usage != nil
		if source.record != nil && isAccepted && hasUsageUpdate {
			updateRecordProtocolUsage(source.record, source.replay.Usage())
		}
	}
	return source.consume(decoded, frame.Metadata)
}

func (source *protocolSourceStream) Finish(readErr error) error {
	for _, frame := range source.state.Finish(readErr) {
		if err := source.decode(frame); err != nil {
			return err
		}
	}
	if readErr != nil {
		return protocol.AnnotateResourceError(readErr, source.candidate.compiled.Identity(), protocol.DecodeEvent, "read.event")
	}
	if source.wireValidation != nil {
		if err := source.wireValidation.Finish(); err != nil {
			return err
		}
	}
	return source.candidate.compiled.AnnotateSourceEventError(source.replay.Finish())
}
