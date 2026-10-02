package protocol

import (
	"context"
	"strconv"
)

// SessionAdapter pins the two immutable revisions and their model contract for
// the entire connection. The transport coordinator is its single owner.
type SessionAdapter struct {
	ingress, upstream                  *Compiled
	clientOperation, upstreamOperation Operation
	binding                            Binding
	options                            EvaluationContext
	model                              Value
	replay                             *SessionReplay
	mediaSequence                      map[EventOrigin]int64
}

// NewSessionAdapter constructs the shared offline/live duplex translation path.
// The caller supplies its already-authorized model group and upstream scope.
func NewSessionAdapter(ingress, upstream *Compiled, clientOperation, upstreamOperation Operation, binding Binding, scope Scope, model Value) (*SessionAdapter, error) {
	if err := CheckSessionCompatibility(clientOperation, upstreamOperation); err != nil {
		return nil, err
	}
	options := EvaluationContext{Scope: scope}
	limits := ingress.limits
	limits.StateItems = min(limits.StateItems, upstream.limits.StateItems)
	limits.BufferBytes = min(limits.BufferBytes, upstream.limits.BufferBytes)
	replay, err := NewSessionReplay(sessionLaneTarget(ingress, ClientEvent, options), sessionLaneTarget(upstream, UpstreamEvent, options), limits, SessionPolicy{Model: model, CanGenerateAutomatically: upstreamOperation.Session.CanGenerateAutomatically})
	if err != nil {
		return nil, err
	}
	return &SessionAdapter{ingress: ingress, upstream: upstream, clientOperation: clientOperation, upstreamOperation: upstreamOperation, binding: binding, options: options, model: model, replay: replay, mediaSequence: map[EventOrigin]int64{}}, nil
}

// Convert retains binary bytes only for explicitly matched formats; JSON uses
// the same compiled directions and model checks as offline verification.
func (adapter *SessionAdapter) Convert(ctx context.Context, origin EventOrigin, frame SessionFrame) ([]SessionFrame, error) {
	if frame.IsBinary {
		return adapter.convertMedia(origin, frame)
	}
	value, err := ParseValue(frame.Payload)
	if err != nil {
		return nil, streamIssue(InvalidInput, "/frame", "session text frame must contain one JSON value")
	}
	source, target, decoder, encoder := adapter.upstream, adapter.ingress, DecodeEvent, EncodeEvent
	if origin == ClientEvent {
		source, target, decoder, encoder = adapter.ingress, adapter.upstream, DecodeClientEvent, EncodeUpstreamEvent
	}
	events, err := source.decodeEvents(ctx, decoder, value, adapter.options)
	if err != nil {
		return nil, err
	}
	frames := make([]SessionFrame, 0, len(events))
	for _, event := range events {
		if err := adapter.checkModel(origin, event); err != nil {
			return nil, err
		}
		accepted, err := adapter.replay.Consume(origin, event)
		if err != nil {
			return nil, err
		}
		if !accepted {
			continue
		}
		if event.Request != nil && !event.Request.Model.IsZero() {
			if origin == ClientEvent {
				event.Request.Model = StringValue(adapter.options.Scope.Model)
			} else {
				event.Request.Model = adapter.model
			}
		}
		if event.Response != nil && !event.Response.Model.IsZero() {
			event.Response.Model = adapter.model
		}
		encoded, err := target.encodeEvent(ctx, encoder, event, adapter.options)
		if err != nil {
			return nil, err
		}
		frames = append(frames, SessionFrame{Payload: encoded.Bytes()})
	}
	return frames, nil
}

func (adapter *SessionAdapter) checkModel(origin EventOrigin, event Event) error {
	if origin == ClientEvent {
		return IssuesError(CheckClientEvent(event, adapter.upstream, adapter.binding, adapter.options.Scope))
	}
	return IssuesError(CheckModelEvent(event, adapter.upstream, adapter.binding, adapter.options.Scope))
}

func (adapter *SessionAdapter) convertMedia(origin EventOrigin, frame SessionFrame) ([]SessionFrame, error) {
	format := adapter.upstreamOperation.Session.OutputMedia
	if origin == ClientEvent {
		format = adapter.clientOperation.Session.InputMedia
	}
	if format == nil {
		return nil, streamIssue(UnsupportedCapability, "/session/media", "binary frame has no declared media format")
	}
	adapter.mediaSequence[origin]++
	sequence := adapter.mediaSequence[origin]
	event := Event{SchemaVersion: SemanticSchemaVersion, Type: MediaReceived, Media: &Media{Type: format.Type, Format: format.Format, Sequence: Value{raw: strconv.FormatInt(sequence, 10)}, Reference: Resource{Kind: "transport_frame", ID: StringValue(string(origin) + ":" + strconv.FormatInt(sequence, 10)), Scope: adapter.options.Scope}}}
	if err := adapter.checkModel(origin, event); err != nil {
		return nil, err
	}
	if _, err := adapter.replay.Consume(origin, event); err != nil {
		return nil, err
	}
	return []SessionFrame{frame}, nil
}

// Finish validates the observed session terminal instead of inventing one on EOF.
func (adapter *SessionAdapter) Finish() error { return adapter.replay.Finish() }

// IsClosed reports an explicit close/failure, never a completed single response.
func (adapter *SessionAdapter) IsClosed() bool { return adapter.replay.isClosed }

// HasFailed distinguishes a forwarded connection-level failure from clean close.
func (adapter *SessionAdapter) HasFailed() bool { return adapter.replay.hasFailed }

// Responses supplies independent terminal/usage snapshots for persistence.
func (adapter *SessionAdapter) Responses() []SessionResponse { return adapter.replay.Responses() }

// ResponseUsage returns snapshots suitable for one settlement per response.
func (adapter *SessionAdapter) ResponseUsage() map[string]*Usage {
	return adapter.replay.ResponseUsage()
}

// EncodeFailure exposes late conversion failures through the declared client
// error event. A protocol without such an encoding still receives an error close.
func (adapter *SessionAdapter) EncodeFailure(ctx context.Context, failure error) (SessionFrame, error) {
	errorValue, err := EncodeValue(map[string]string{"code": "protocol_session_error", "message": failure.Error()})
	if err != nil {
		return SessionFrame{}, err
	}
	frame, err := adapter.ingress.EncodeEvent(ctx, Event{SchemaVersion: SemanticSchemaVersion, Type: OperationFailed, Error: errorValue}, adapter.options)
	return SessionFrame{Payload: frame.Bytes()}, err
}
