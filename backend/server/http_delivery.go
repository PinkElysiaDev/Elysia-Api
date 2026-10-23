package server

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

const (
	httpDeliveryKey      = "elysia.httpDelivery"
	deliveryCompleted    = "completed"
	deliveryFailed       = "failed"
	deliveryWrite        = "response_write"
	deliveryFlush        = "response_flush"
	deliveryFinalize     = "response_finalize"
	deliveryAbort        = "response_abort"
	deliveryDisconnected = "downstream_disconnected"
	deliveryShutdown     = "server_shutdown"
	deliveryDeadline     = "deadline"
	deliveryCanceled     = "request_canceled"
	deliveryTransport    = "downstream_transport"
)

var (
	errDownstreamDisconnected = errors.New("downstream connection disconnected")
	errHTTPServerShutdown     = errors.New("HTTP server is shutting down")
)

// httpDelivery describes the server's observed delivery, not an acknowledgement
// that a remote application consumed the response. HTTPStatus is the selected
// wire status; usageRecord.StatusCode remains the logical call result.
type httpDelivery struct {
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Outcome    string `json:"outcome"`
	Phase      string `json:"phase,omitempty"`
	Cause      string `json:"cause,omitempty"`
	Error      string `json:"error,omitempty"`
}

// The completion owner freezes evidence before any cleanup cancels contexts.
// This state never holds a request body, route plan or a pooled context.
type httpDeliveryState struct {
	ctx    context.Context
	result *httpDelivery
	err    error
}

func trackHTTPDelivery(ctx context.Context, c *app.RequestContext) *httpDeliveryState {
	if value, ok := c.Get(httpDeliveryKey); ok {
		state := value.(*httpDeliveryState)
		if ctx != nil && state.result == nil {
			state.ctx = ctx
		}
		return state
	}
	state := &httpDeliveryState{ctx: ctx}
	c.Set(httpDeliveryKey, state)
	return state
}

func freezeHTTPDelivery(c *app.RequestContext) *httpDeliveryState {
	state := trackHTTPDelivery(nil, c)
	if state.result != nil {
		return state
	}
	result := &httpDelivery{HTTPStatus: c.Response.StatusCode(), Outcome: deliveryCompleted}
	var cause error
	if state.ctx != nil {
		cause = context.Cause(state.ctx)
	}
	state.ctx = nil
	if trace := c.GetTraceInfo(); trace != nil {
		state.err = trace.Stats().Error()
	}
	if state.err == nil {
		if stream := currentResponseStream(c); stream != nil {
			state.err = stream.err
		}
	}
	if cause != nil {
		result.Cause = deliveryFailureCause(cause, nil)
	}
	if state.err != nil {
		result.Outcome = deliveryFailed
		result.Cause = deliveryFailureCause(cause, state.err)
		result.Phase = "response"
		var failure *downstreamWriteError
		if errors.As(state.err, &failure) {
			result.Phase = failure.phase
		}
		result.Error = truncateForDisplay(state.err.Error(), errorDetailTruncateBytes)
	}
	state.result = result
	return state
}

// reported is only accessed by Hertz's connection owner: Finish emits the
// request log before Engine.Serve asks HandleSpecificError about the same error.
type downstreamWriteError struct {
	cause      error
	phase      string
	isReported bool
}

// Socket unavailability stops outstanding work but does not establish who
// closed the connection. Preserve this distinction across the polling race.
type downstreamObservationError struct{ cause error }

func (failure *downstreamObservationError) Error() string { return failure.cause.Error() }
func (failure *downstreamObservationError) Unwrap() error { return failure.cause }

func (failure *downstreamWriteError) Error() string { return failure.cause.Error() }
func (failure *downstreamWriteError) Unwrap() error { return failure.cause }

func wrapDownstreamError(err error, phase string) error {
	if err == nil {
		return nil
	}
	var failure *downstreamWriteError
	if errors.As(err, &failure) {
		if phase == deliveryFinalize {
			failure.phase = phase
		}
		return err
	}
	return &downstreamWriteError{cause: err, phase: phase}
}

func deliveryFailureCause(cause, err error) string {
	switch {
	case errors.Is(err, errResponseAborted):
		return "server_panic"
	case errors.Is(cause, errHTTPServerShutdown):
		return deliveryShutdown
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return deliveryDeadline
	case errors.Is(cause, errDownstreamDisconnected):
		return deliveryDisconnected
	case errors.Is(cause, context.Canceled):
		return deliveryCanceled
	}
	var observation *downstreamObservationError
	if errors.As(cause, &observation) {
		return deliveryTransport
	}
	if cause != nil || errors.Is(err, context.Canceled) {
		return deliveryCanceled
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return deliveryDeadline
	}
	return deliveryTransport
}

func deliveryFailureStatus(cause string) (int, string) {
	switch cause {
	case deliveryDisconnected:
		return statusClientClosedRequest, ErrorKindClientCanceled
	case deliveryCanceled:
		return http.StatusServiceUnavailable, deliveryCanceled
	case deliveryShutdown:
		return http.StatusServiceUnavailable, deliveryShutdown
	case deliveryDeadline:
		return http.StatusGatewayTimeout, "request_timeout"
	case "server_panic":
		return http.StatusInternalServerError, "server_error"
	default:
		return http.StatusBadGateway, deliveryTransport
	}
}

func hasObservedPrimaryFailure(record *usageRecord, err error) bool {
	var httpFailure *upstreamHTTPFailure
	var generation *protocol.GenerationFailure
	var resource *protocol.ResourceLimitError
	var idle *relay.StreamIdleError
	return record.UpstreamHTTPStatus >= http.StatusBadRequest || record.TransportFailure != nil || errors.As(err, &idle) ||
		errors.As(err, &httpFailure) || errors.As(err, &generation) ||
		independentConversionIssue(err) != nil || errors.As(err, &resource)
}

func independentConversionIssue(err error) *protocol.ConversionIssue {
	if failure, ok := err.(*protocol.ConversionError); ok {
		for index := range failure.Issues {
			issue := &failure.Issues[index]
			if issue.Severity == protocol.SeverityError && issue.Code != protocol.ExecutionCancelled && issue.Code != protocol.ExecutionTimeout {
				return issue
			}
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if issue := independentConversionIssue(child); issue != nil {
				return issue
			}
		}
	case interface{ Unwrap() error }:
		return independentConversionIssue(wrapped.Unwrap())
	}
	return nil
}

// Request cancellation and downstream delivery must not replace an already
// observed provider, HTTP or protocol failure.
func classifyRequestInterruption(record *usageRecord, ctx context.Context, err error) {
	if hasObservedPrimaryFailure(record, err) {
		return
	}
	var downstream *downstreamWriteError
	if ctx.Err() == nil && !errors.As(err, &downstream) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return
	}
	record.StatusCode, record.ErrorKind = deliveryFailureStatus(deliveryFailureCause(context.Cause(ctx), err))
}
