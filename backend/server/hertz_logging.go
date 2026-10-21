package server

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/elysia-api/backend/logging"
	"github.com/rs/zerolog"
)

var hertzLoggingOnce sync.Once

// Framework diagnostics follow the process logger configured by main. Resolve
// it at emission time so constructing test servers never replaces global output
// with a request-specific logger or a store that may already have been closed.
type hertzLogger struct{ level atomic.Int32 }

func (l *hertzLogger) SetLevel(level hlog.Level) { l.level.Store(int32(level)) }
func (l *hertzLogger) SetOutput(w io.Writer)     { logging.Default().SetOutput(w) }
func (l *hertzLogger) emit(ctx context.Context, level hlog.Level, format string, args ...any) {
	if int32(level) < l.level.Load() {
		return
	}
	severity := zerolog.InfoLevel
	switch level {
	case hlog.LevelTrace:
		severity = zerolog.TraceLevel
	case hlog.LevelDebug:
		severity = zerolog.DebugLevel
	case hlog.LevelWarn:
		severity = zerolog.WarnLevel
	case hlog.LevelError, hlog.LevelFatal:
		severity = zerolog.ErrorLevel
	}
	logger := logging.Default()
	if !logger.Enabled(severity) {
		return
	}
	message := fmt.Sprintf(format, args...)
	event := logger.Event(severity)
	if ctx != nil {
		if requestLogger := zerolog.Ctx(ctx); requestLogger.GetLevel() != zerolog.Disabled {
			event = requestLogger.WithLevel(severity)
		}
	}
	event.Str("component", "hertz").Msg(message)
}

func (l *hertzLogger) Trace(v ...any)             { l.emit(nil, hlog.LevelTrace, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Debug(v ...any)             { l.emit(nil, hlog.LevelDebug, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Info(v ...any)              { l.emit(nil, hlog.LevelInfo, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Notice(v ...any)            { l.emit(nil, hlog.LevelNotice, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Warn(v ...any)              { l.emit(nil, hlog.LevelWarn, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Error(v ...any)             { l.emit(nil, hlog.LevelError, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Fatal(v ...any)             { l.emit(nil, hlog.LevelFatal, "%s", fmt.Sprint(v...)) }
func (l *hertzLogger) Tracef(f string, v ...any)  { l.emit(nil, hlog.LevelTrace, f, v...) }
func (l *hertzLogger) Debugf(f string, v ...any)  { l.emit(nil, hlog.LevelDebug, f, v...) }
func (l *hertzLogger) Infof(f string, v ...any)   { l.emit(nil, hlog.LevelInfo, f, v...) }
func (l *hertzLogger) Noticef(f string, v ...any) { l.emit(nil, hlog.LevelNotice, f, v...) }
func (l *hertzLogger) Warnf(f string, v ...any)   { l.emit(nil, hlog.LevelWarn, f, v...) }
func (l *hertzLogger) Errorf(f string, v ...any)  { l.emit(nil, hlog.LevelError, f, v...) }
func (l *hertzLogger) Fatalf(f string, v ...any)  { l.emit(nil, hlog.LevelFatal, f, v...) }
func (l *hertzLogger) CtxTracef(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelTrace, f, v...)
}
func (l *hertzLogger) CtxDebugf(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelDebug, f, v...)
}
func (l *hertzLogger) CtxInfof(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelInfo, f, v...)
}
func (l *hertzLogger) CtxNoticef(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelNotice, f, v...)
}
func (l *hertzLogger) CtxWarnf(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelWarn, f, v...)
}
func (l *hertzLogger) CtxErrorf(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelError, f, v...)
}
func (l *hertzLogger) CtxFatalf(c context.Context, f string, v ...any) {
	l.emit(c, hlog.LevelFatal, f, v...)
}
