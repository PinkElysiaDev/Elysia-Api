package server

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/protocol/http1/resp"
	"github.com/cloudwego/hertz/pkg/protocol/sse"
)

const responseStreamKey = "elysia.responseStream"

const httpPanicResponse = "internal server error"

var errResponseAborted = errors.New("response aborted by request panic")

// responseStream owns the native chunk writer and its commit boundary. All
// methods run on the handler goroutine; Hertz finalizes it after the handler.
type responseStream struct {
	network.ExtWriter
	capture   *downstreamCapture
	committed bool
	err       error
}

func (w *responseStream) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	// Hertz writes the headers even for an empty Write. Conservatively prevent
	// retries as soon as a write is attempted, including a partial socket error.
	w.committed = true
	n, err := w.ExtWriter.Write(p)
	if w.capture != nil {
		w.capture.capture(p[:n])
	}
	w.err = wrapDownstreamError(err, deliveryWrite)
	return n, w.err
}

func (w *responseStream) Flush() error {
	if w.err != nil {
		return w.err
	}
	// Native chunkWriter.Flush only flushes the socket; write the headers first
	// so an explicit Flush has a well-defined commitment boundary.
	if !w.committed {
		if _, err := w.Write(nil); err != nil {
			return err
		}
	}
	w.err = wrapDownstreamError(w.ExtWriter.Flush(), deliveryFlush)
	return w.err
}

func (w *responseStream) Finalize() error {
	if w.err != nil {
		return w.err
	}
	w.err = wrapDownstreamError(w.ExtWriter.Finalize(), deliveryFinalize)
	return w.err
}

func currentResponseStream(c *app.RequestContext) *responseStream {
	if value, ok := c.Get(responseStreamKey); ok {
		return value.(*responseStream)
	}
	return nil
}

func responseCommitted(c *app.RequestContext) bool {
	w := currentResponseStream(c)
	return w != nil && w.committed
}

func startStream(c *app.RequestContext) network.ExtWriter {
	if writer := c.Response.GetHijackWriter(); writer != nil {
		return writer
	}
	w := &responseStream{ExtWriter: resp.NewChunkedBodyWriter(&c.Response, c.GetWriter())}
	if capture, ok := c.Get("downstreamCapture"); ok {
		w.capture = capture.(*downstreamCapture)
		w.capture.streaming = true
	}
	c.Set(responseStreamKey, w)
	c.Response.HijackWriter(w)
	return w
}

// The native SSE writer reuses our installed ExtWriter, so event formatting and
// flushing belong to Hertz while capture, panic and retry boundaries stay shared
// with other streams. Hertz finalizes the writer when the handler returns.
func startSSE(c *app.RequestContext) *sse.Writer {
	startStream(c)
	c.Header("X-Accel-Buffering", "no")
	return sse.NewWriter(c)
}

// An unused stream can still become a buffered HTTP error. Detach both the
// writer and its bookkeeping so a later stream cannot inherit stale state.
func discardUncommittedStream(c *app.RequestContext) {
	if responseCommitted(c) {
		return
	}
	c.Response.HijackWriter(nil)
	c.Set(responseStreamKey, (*responseStream)(nil))
	c.Response.Header.Del("Trailer")
	c.Response.Header.Trailer().Reset()
	if value, ok := c.Get("downstreamCapture"); ok {
		value.(*downstreamCapture).streaming = false
	}
}

func recoverHTTPResponse(c *app.RequestContext) {
	c.Abort()
	state := trackHTTPDelivery(nil, c)
	state.applicationError = errResponseAborted
	if w := currentResponseStream(c); w != nil && w.committed {
		// Do not append a successful zero chunk after a panic or rewrite headers
		// that are already on the wire. Returning an error closes the connection.
		w.err = wrapDownstreamError(errResponseAborted, deliveryAbort)
		c.SetConnectionClose()
		return
	}
	discardUncommittedStream(c)
	c.Response.ResetBody()
	c.Response.Header.Del("Content-Type")
	c.String(500, httpPanicResponse)
	state.bufferedErrorBody = httpPanicResponse
}
