package server

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/elysia-api/backend/logging"

	"github.com/rs/zerolog"
)

func (s *Server) logVerbose(format string, args ...any) {
	if s.config.IsVerboseLog() {
		s.logEvent("debug").Msgf(format, args...)
	}
}

// Operation events bypass the diagnostic persistence threshold.
func (s *Server) logSystemEvent(level, message string, fields map[string]any) {
	l := s.Logger().Component("server")
	l.WithLevel(logging.ParseLevel(level)).Fields(fields).Str("event_kind", "operation").Msg(message)
}

func (s *Server) Logger() *logging.Logger {
	if s.logger != nil {
		return s.logger
	}
	return logging.Default()
}

func (s *Server) logEvent(level string) *zerolog.Event {
	return s.Logger().Event(logging.ParseLevel(level)).Str("component", "server")
}

func (s *Server) requestLogging() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		started := time.Now()
		id := usageRequestID(started)
		c.Set("requestID", id)
		c.Header("X-Elysia-Request-Id", id)
		logger := s.Logger().Component("http").With().Str("request_id", id).Logger()
		ctx = logger.WithContext(ctx)
		// Middleware return only ends application dispatch. Hertz still owns
		// BodyStream/hijack I/O; log from Finish before the lifecycle lease exits.
		onHTTPComplete(c, func() {
			var transportErr error
			if trace := c.GetTraceInfo(); trace != nil {
				transportErr = trace.Stats().Error()
			}
			level := zerolog.DebugLevel
			if c.Response.StatusCode() >= 500 || transportErr != nil {
				level = zerolog.ErrorLevel
			}
			if s.Logger().Enabled(level) {
				logger.WithLevel(level).Err(transportErr).
					Str("method", string(c.Method())).Str("path", string(c.Path())).
					Int("status_code", c.Response.StatusCode()).Int64("duration_ms", time.Since(started).Milliseconds()).
					Msg("request completed")
			}
		})
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error().Interface("panic", recovered).Bytes("stack", debug.Stack()).Msg("request panic")
				recoverHTTPResponse(c)
			}
		}()
		c.Next(ctx)
	}
}
