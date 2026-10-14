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
	idle   time.Duration
}

// limitStreamRead cancels the HTTP request itself so a stalled network read
// cannot leave a goroutine behind. Consumer processing time is not upstream idle.
func limitStreamRead(cancel context.CancelCauseFunc, idle time.Duration, phase StreamIdlePhase) func() {
	fired := make(chan struct{})
	timer := time.AfterFunc(idle, func() { cancel(&StreamIdleError{Phase: phase}); close(fired) })
	return func() {
		if !timer.Stop() {
			<-fired
		}
	}
}

func (transport *ProtocolTransport) sendStreamRequest(request *http.Request, idle time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(request.Context())
	stop := limitStreamRead(cancel, idle, StreamAwaitingResponse)
	response, err := transport.streamClient.Do(request.WithContext(ctx))
	stop()
	if err != nil || context.Cause(ctx) != nil {
		err = errors.Join(sanitizeTransportError(err), context.Cause(ctx))
		cancel(nil)
		if response != nil {
			response.Body.Close()
		}
		return nil, err
	}
	response.Body = &streamBody{ReadCloser: response.Body, ctx: ctx, cancel: cancel, idle: idle}
	return response, nil
}

func (body *streamBody) Read(buffer []byte) (int, error) {
	stop := limitStreamRead(body.cancel, body.idle, StreamReadingBody)
	count, err := body.ReadCloser.Read(buffer)
	stop()
	if cause := context.Cause(body.ctx); cause != nil {
		return count, errors.Join(err, cause)
	}
	return count, err
}

func (body *streamBody) Close() error {
	body.cancel(nil)
	return body.ReadCloser.Close()
}
