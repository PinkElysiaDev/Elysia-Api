package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

var errStreamIdle = errors.New("upstream stream idle timeout")

// StreamIdlePhase identifies the observable HTTP boundary, not an inferred
// network cause such as DNS, TLS or provider processing.
type StreamIdlePhase string

const (
	StreamAwaitingResponse StreamIdlePhase = "awaiting_response"
	StreamReadingBody      StreamIdlePhase = "reading_body"
)

// StreamIdleError is the request-local idle deadline. It is distinct from a
// caller cancellation and does not change the HTTP status or retry policy.
type StreamIdleError struct{ Phase StreamIdlePhase }

func (err *StreamIdleError) Error() string {
	return errStreamIdle.Error() + " (" + string(err.Phase) + ")"
}
func (err *StreamIdleError) Unwrap() error { return errStreamIdle }

type streamBody struct {
	io.ReadCloser
	ctx    context.Context
	cancel context.CancelCauseFunc
	idle   *streamIdleTimer
}

// One timer belongs to the sequential HTTP exchange/read owner. stop waits for
// an expired callback before the phase or timer can be reused. Close only
// cancels the request, so it can safely interrupt an outstanding Read.
type streamIdleTimer struct {
	timer *time.Timer
	fired chan struct{}
	idle  time.Duration
	phase StreamIdlePhase
}

func newStreamIdleTimer(cancel context.CancelCauseFunc, idle time.Duration) *streamIdleTimer {
	state := &streamIdleTimer{fired: make(chan struct{}, 1), idle: idle, phase: StreamAwaitingResponse}
	state.timer = time.AfterFunc(idle, func() {
		cancel(&StreamIdleError{Phase: state.phase})
		state.fired <- struct{}{}
	})
	return state
}

func (state *streamIdleTimer) stop() {
	if !state.timer.Stop() {
		<-state.fired
	}
}

func (transport *ProtocolTransport) sendStreamRequest(request *http.Request, idle time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(request.Context())
	timer := newStreamIdleTimer(cancel, idle)
	response, err := transport.streamClient.Do(request.WithContext(ctx))
	timer.stop()
	if err != nil || context.Cause(ctx) != nil {
		err = errors.Join(sanitizeTransportError(err), context.Cause(ctx))
		cancel(nil)
		if response != nil {
			response.Body.Close()
		}
		return nil, err
	}
	response.Body = &streamBody{ReadCloser: response.Body, ctx: ctx, cancel: cancel, idle: timer}
	return response, nil
}

func (body *streamBody) Read(buffer []byte) (int, error) {
	body.idle.phase = StreamReadingBody
	body.idle.timer.Reset(body.idle.idle)
	count, err := body.ReadCloser.Read(buffer)
	body.idle.stop()
	if cause := context.Cause(body.ctx); cause != nil {
		return count, errors.Join(err, cause)
	}
	return count, err
}

func (body *streamBody) Close() error {
	body.cancel(nil)
	return body.ReadCloser.Close()
}
