package server

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
)

// httpRequests owns admission, forced socket interruption and handler draining.
// Stop must precede Wait so no WaitGroup.Add can race with an empty Wait.
type httpRequests struct {
	mu      sync.Mutex
	workers sync.WaitGroup
	active  map[*app.RequestContext]net.Conn
	stopped bool
}

func (r *httpRequests) enter(c *app.RequestContext, conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	if r.active == nil {
		r.active = make(map[*app.RequestContext]net.Conn)
	}
	r.active[c] = conn
	r.workers.Add(1)
	return true
}

func (r *httpRequests) leave(c *app.RequestContext) {
	r.mu.Lock()
	delete(r.active, c)
	r.mu.Unlock()
	r.workers.Done()
}

func (r *httpRequests) stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

func (r *httpRequests) interrupt() {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Close the raw net.Conn supplied by OnAccept, not Hertz's buffered Conn.
	// net.Conn supports concurrent methods; Hertz alone releases its I/O buffers
	// and disconnect monitor after the response/hijack has finished.
	for _, conn := range r.active {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func (r *httpRequests) wait() { r.workers.Wait() }

func (s *Server) requestLifecycle() app.HandlerFunc {
	s.initLifecycle()
	return func(ctx context.Context, c *app.RequestContext) {
		conn, _ := ctx.Value(httpSocketKey{}).(net.Conn)
		if !s.httpRequests.enter(c, conn) {
			c.AbortWithMsg("server is shutting down", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithCancelCause(ctx)
		stop := context.AfterFunc(s.lifecycleCtx, func() { cancel(errHTTPServerShutdown) })
		trackHTTPDelivery(ctx, c)
		onHTTPComplete(c, func() { stop(); cancel(nil); s.httpRequests.leave(c) })
		if s.lifecycleCtx.Err() != nil {
			cancel(errHTTPServerShutdown)
		}
		c.Next(ctx)
	}
}

type httpSocketKey struct{}

const httpCompletionKey = "elysia.httpCompletion"

// Hertz invokes Finish after response serialization (including SetBodyStream
// and hijacked handlers), before returning RequestContext to its pool. Do not
// model this lifetime with middleware defers or a goroutine per request.
type httpCompletionTracer struct{}

func (httpCompletionTracer) Start(ctx context.Context, _ *app.RequestContext) context.Context {
	return ctx
}
func (httpCompletionTracer) Finish(_ context.Context, c *app.RequestContext) { finishHTTPRequest(c) }

func onHTTPComplete(c *app.RequestContext, done func()) {
	callbacks, _ := c.Get(httpCompletionKey)
	if callbacks == nil {
		c.Set(httpCompletionKey, []func(){done})
		return
	}
	c.Set(httpCompletionKey, append(callbacks.([]func()), done))
}

func finishHTTPRequest(c *app.RequestContext) {
	freezeHTTPDelivery(c)
	value, ok := c.Get(httpCompletionKey)
	if !ok || value == nil {
		return
	}
	c.Set(httpCompletionKey, nil)
	// Defer preserves reverse registration order and unwinds every release even
	// if another callback panics. Hertz's tracer controller reports the panic
	// after our lifecycle lease and other request resources have been released.
	for _, callback := range value.([]func()) {
		defer callback()
	}
}
