package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/network"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

type finalizeFailureWriter struct {
	network.ExtWriter
	err   error
	calls int
}

func TestConversionCancellationDoesNotHideItsOrigin(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errDownstreamDisconnected)
	cancellation := errors.Join(context.Canceled, protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.ExecutionCancelled, Severity: protocol.SeverityError, Path: "/"}}))
	record := &usageRecord{StatusCode: 200}
	setUsageError(record, ctx, cancellation)
	if record.StatusCode != 499 || record.ErrorKind != ErrorKindClientCanceled {
		t.Fatal(record.StatusCode, record.ErrorKind)
	}
	independent := protocol.IssuesError([]protocol.ConversionIssue{{Code: protocol.UnsupportedNative, Severity: protocol.SeverityError, Path: "/payload"}})
	record = &usageRecord{StatusCode: 400}
	setUsageError(record, ctx, errors.Join(cancellation, independent))
	if record.StatusCode != 400 || record.ErrorKind != string(protocol.UnsupportedNative) {
		t.Fatal("lost an independent protocol failure", record.StatusCode, record.ErrorKind)
	}
}

func (writer *finalizeFailureWriter) Finalize() error {
	writer.calls++
	return writer.err
}

func TestResponseStreamRemembersFinalizeFailure(t *testing.T) {
	cause := errors.New("synthetic final chunk failure")
	native := &finalizeFailureWriter{err: cause}
	stream := &responseStream{ExtWriter: native}
	for range 2 {
		if err := stream.Finalize(); !errors.Is(err, cause) {
			t.Fatal("finalization lost the original cause", err)
		}
	}
	if native.calls != 1 || !errors.Is(stream.err, cause) {
		t.Fatal("finalization was repeated or its failure was not retained", native.calls, stream.err)
	}
}

func TestGatewayCancellationPreservesObservedHTTPFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cause := errors.New("synthetic upstream HTTP failure")
	failure := &upstreamHTTPFailure{&gatewayFailure{status: 524, cause: cause}}
	record := &usageRecord{UpstreamHTTPStatus: 524}
	output := httptest.NewRecorder()
	request := newTestContext(output)
	server := &Server{}
	server.failGateway(ctx, request, record, http.StatusBadGateway, failure)
	if record.StatusCode != 524 || record.ErrorKind == ErrorKindClientCanceled {
		t.Fatal("later cancellation replaced an observed upstream failure", record.StatusCode, record.ErrorKind)
	}
}

func TestGatewayDeadlineIsNotClientCancellation(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	record := &usageRecord{}
	server := &Server{}
	server.failGateway(ctx, newTestContext(httptest.NewRecorder()), record, http.StatusBadGateway, ctx.Err())
	if record.StatusCode != http.StatusGatewayTimeout || record.ErrorKind == ErrorKindClientCanceled {
		t.Fatal("deadline was attributed to the client", record.StatusCode, record.ErrorKind)
	}
}

func TestHTTPCompletionFreezesBeforeCleanupCancellation(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		ctx, cancel := context.WithCancelCause(t.Context())
		request := newTestContext(httptest.NewRecorder())
		state := trackHTTPDelivery(ctx, request)
		if cancelBefore {
			cancel(errHTTPServerShutdown)
		}
		onHTTPComplete(request, func() { cancel(errHTTPServerShutdown) })
		finishHTTPRequest(request)
		if state.result.Outcome != deliveryCompleted || state.err != nil || state.ctx != nil {
			t.Fatal("cleanup or a late cancellation changed successful transport", state)
		}
		if (state.result.Cause == deliveryShutdown) != cancelBefore {
			t.Fatal("completion did not preserve the observed cancellation boundary", state.result)
		}
	}
}

func TestHTTPDeliveryLookupPreservesTrackedCause(t *testing.T) {
	request := newTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errHTTPServerShutdown)
	state := trackHTTPDelivery(ctx, request)
	if ensureHTTPDeliveryState(request) != state || state.ctx != ctx {
		t.Fatal("state lookup replaced its cancellation context")
	}
	freezeHTTPDelivery(request)
	trackHTTPDelivery(context.Background(), request)
	if state.ctx != nil || state.result.Cause != deliveryShutdown {
		t.Fatal("tracking after completion changed frozen evidence")
	}
}

func TestDownstreamClosureWithoutPeerEvidenceRemainsUnknown(t *testing.T) {
	for _, cause := range []error{io.EOF, io.ErrClosedPipe, syscall.EPIPE, net.ErrClosed} {
		record := &usageRecord{StatusCode: 200}
		setUsageError(record, t.Context(), wrapDownstreamError(cause, deliveryFlush))
		if record.StatusCode != 502 || record.ErrorKind != deliveryTransport {
			t.Fatal("an ambiguous closure became a peer disconnect", cause, record.StatusCode, record.ErrorKind)
		}
	}
}

func TestRequestInterruptionSourcesAndPrimaryTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status int
		kind   string
	}{
		{"disconnect", errDownstreamDisconnected, 499, ErrorKindClientCanceled},
		{"shutdown", errHTTPServerShutdown, 503, deliveryShutdown},
		{"unknown-cancel", context.Canceled, 503, deliveryCanceled},
		{"deadline", context.DeadlineExceeded, 504, "request_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(tc.cause)
			err := wrapDownstreamError(errors.New("synthetic write failure"), deliveryFlush)
			record := &usageRecord{StatusCode: 200}
			setUsageError(record, ctx, err)
			if record.StatusCode != tc.status || record.ErrorKind != tc.kind {
				t.Fatal(record.StatusCode, record.ErrorKind)
			}
			idle := &relay.StreamIdleError{Phase: relay.StreamReadingBody}
			record = &usageRecord{StatusCode: 200}
			setUsageError(record, ctx, idle)
			if record.ErrorKind != ErrorKindUpstreamIdleTimeout || record.StatusCode != 502 {
				t.Fatal("later cancellation replaced the upstream timeout", record.StatusCode, record.ErrorKind)
			}
		})
	}
}

func TestDeliveryPanicPrecedesConcurrentCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, errHTTPServerShutdown, errDownstreamDisconnected} {
		kind := deliveryFailureCause(cause, wrapDownstreamError(errResponseAborted, deliveryAbort))
		if status, _ := deliveryFailureStatus(kind); status != 500 || kind != "server_panic" {
			t.Fatal("cancellation replaced a known panic", status, kind)
		}
	}
}
