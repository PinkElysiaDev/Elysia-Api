package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/app"
	hserver "github.com/cloudwego/hertz/pkg/app/server"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/common/tracer/stats"
	"github.com/cloudwego/hertz/pkg/protocol/http1/ext"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func newHTTPEngine(cfg *config.Config, extra ...hconfig.Option) *route.Engine {
	hertzLoggingOnce.Do(func() { hlog.SetLogger(&hertzLogger{}) })
	disconnect := newPlatformDisconnectMonitor()
	options := []hconfig.Option{
		// Large multimodal bodies need bounded streaming reads. Windows supplies
		// its own socket observation because connstate does not support it.
		hserver.WithTransport(disconnect.transport),
		hserver.WithOnAccept(func(conn net.Conn) context.Context {
			return context.WithValue(context.Background(), httpSocketKey{}, conn)
		}),
		hserver.WithTracer(httpCompletionTracer{}),
		hserver.WithTraceLevel(stats.LevelDisabled),
		hserver.WithStreamBody(true),
		// Enter the handler after a bounded prefix, before buffering full payloads.
		// The application reserves capacity before reading and enforces its own
		// hot-reloadable, per-protocol body budgets.
		hserver.WithMaxRequestBodySize(1),
		hserver.WithMaxKeepBodySize(64 << 10),
		hserver.WithDisablePreParseMultipartForm(true),
		hserver.WithSenseClientDisconnection(disconnect == nil),
		hserver.WithDisablePrintRoute(true),
	}
	if cfg != nil {
		listen := cfg.GetServer()
		options = append(options, hserver.WithHostPorts(net.JoinHostPort(listen.Host, strconv.Itoa(listen.Port))))
	}
	engine := hserver.New(append(options, extra...)...).Engine
	if disconnect != nil {
		engine.Use(disconnect.middleware())
	}
	engine.Use(func(ctx context.Context, c *app.RequestContext) {
		trackHTTPDelivery(ctx, c)
		defer func() {
			if !c.Request.IsBodyStream() || c.Hijacked() {
				return
			}
			if value, ok := c.Get("requestBodyReader"); ok && value.(*requestBodyReader).done {
				return
			}
			// Rejected/unused uploads must not be drained after admission ends.
			// Closing the connection also prevents leftover bytes being parsed as
			// a subsequent keep-alive request.
			c.SetConnectionClose()
			// Release the native body-stream pool entry without waiting for the
			// rest of an abandoned upload. Only reads expire; the error response
			// can still be written. Detaching alone would bypass Hertz's recycler.
			_ = c.GetConn().SetReadDeadline(time.Now())
			_ = ext.ReleaseBodyStream(c.RequestBodyStream())
			_ = c.Request.CloseBodyStream()
		}()
		c.Next(ctx)
	})
	return engine
}

type requestBodyReader struct {
	reader    io.Reader
	remaining int64
	done      bool
}

func (r *requestBodyReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if r.remaining >= 0 {
		r.remaining -= int64(n)
		r.done = r.remaining == 0
	}
	if err == io.EOF {
		r.done = true
	}
	return n, err
}

func requestBody(c *app.RequestContext) io.Reader {
	if c.Request.IsBodyStream() {
		if value, ok := c.Get("requestBodyReader"); ok {
			return value.(*requestBodyReader)
		}
		reader := &requestBodyReader{reader: c.RequestBodyStream(), remaining: int64(c.Request.Header.ContentLength())}
		c.Set("requestBodyReader", reader)
		return reader
	}
	return bytes.NewReader(c.Request.Body())
}

func queryValues(c *app.RequestContext, key string) []string {
	values := c.QueryArgs().PeekAll(key)
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func bindJSON(ctx context.Context, c *app.RequestContext, target any) error {
	body, err := protocol.ReadBoundedBody(requestBody(c), protocol.RequestBodyLimit(ctx))
	if err != nil {
		return err
	}
	return sonic.ConfigStd.Unmarshal(body, target)
}
