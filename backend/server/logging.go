package server

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/elysia-api/backend/logging"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func (s *Server) Logger() *logging.Logger {
	if s.logger != nil {
		return s.logger
	}
	return logging.Default()
}

func (s *Server) logEvent(level string) *zerolog.Event {
	return s.Logger().Event(logging.ParseLevel(level)).Str("component", "server")
}

func (s *Server) requestLogging() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		id := usageRequestID(started)
		c.Set("requestID", id)
		c.Header("X-Elysia-Request-Id", id)
		logger := s.Logger().Component("http").With().Str("request_id", id).Logger()
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context()))
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error().Interface("panic", recovered).Bytes("stack", debug.Stack()).Msg("request panic")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
			level := zerolog.DebugLevel
			if c.Writer.Status() >= 500 {
				level = zerolog.ErrorLevel
			}
			if s.Logger().Enabled(level) {
				zerolog.Ctx(c.Request.Context()).WithLevel(level).
					Str("method", c.Request.Method).Str("path", c.Request.URL.Path).
					Int("status_code", c.Writer.Status()).Int64("duration_ms", time.Since(started).Milliseconds()).
					Msg("request completed")
			}
		}()
		c.Next()
	}
}
