// Package logging is the single zerolog entry point for the process. Console
// filtering and database filtering are independent; operation events are always
// offered to persistence. A database failure is reported directly to stderr.
package logging

import (
	"context"
	"io"
	stdlog "log"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
)

type Options struct{ Level, PersistLevel, Format string }

type Logger struct {
	logger       zerolog.Logger
	mu           sync.Mutex
	output       io.Writer
	level        atomic.Int32
	persistLevel atomic.Int32
	sink         *Writer
}

func ParseLevel(value string) zerolog.Level {
	level, err := zerolog.ParseLevel(value)
	if err != nil || level < zerolog.DebugLevel || level > zerolog.ErrorLevel {
		return zerolog.InfoLevel
	}
	return level
}

func New(options Options, sink *Writer) *Logger {
	l := &Logger{sink: sink}
	l.Configure(options)
	l.logger = zerolog.New(l).Level(zerolog.DebugLevel).With().Timestamp().Logger()
	return l
}

func (l *Logger) Configure(options Options) {
	l.level.Store(int32(ParseLevel(options.Level)))
	l.persistLevel.Store(int32(ParseLevel(options.PersistLevel)))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = os.Stderr
	if options.Format == "console" || (options.Format != "json" && isatty.IsTerminal(os.Stderr.Fd())) {
		l.output = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	}
}

func (l *Logger) SetOutput(w io.Writer) { l.mu.Lock(); l.output = w; l.mu.Unlock() }

func (l *Logger) Component(name string) zerolog.Logger {
	return l.logger.With().Str("component", name).Logger()
}

// Enabled lets hot paths skip building diagnostic events when neither output
// accepts them. Explicit operation events bypass this diagnostic predicate.
func (l *Logger) Enabled(level zerolog.Level) bool {
	return level >= zerolog.Level(l.level.Load()) || (l.sink != nil && level >= zerolog.Level(l.persistLevel.Load()))
}

// Event filters diagnostics before their fields and message are encoded.
// Operation events use Component directly to bypass diagnostic thresholds.
func (l *Logger) Event(level zerolog.Level) *zerolog.Event {
	if !l.Enabled(level) {
		return nil
	}
	return l.logger.WithLevel(level)
}

// Standard bridges APIs such as http.Server.ErrorLog, whose public contract
// requires *log.Logger. Formatting, levels and persistence still use zerolog.
func (l *Logger) Standard(component string) *stdlog.Logger {
	return slog.NewLogLogger(zerolog.NewSlogHandler(l.Component(component)), slog.LevelError)
}

func (l *Logger) Write(p []byte) (int, error) { return l.WriteLevel(zerolog.InfoLevel, p) }

func (l *Logger) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	if level >= zerolog.Level(l.level.Load()) {
		l.mu.Lock()
		_, err := l.output.Write(p)
		l.mu.Unlock()
		if err != nil {
			Emergency("console log write failed", err)
		}
	}
	if l.sink != nil {
		important := level >= zerolog.ErrorLevel
		if !important {
			var event struct {
				Kind string `json:"event_kind"`
			}
			// Decode the top-level marker, never match arbitrary message contents.
			_ = sonic.ConfigStd.Unmarshal(p, &event)
			important = event.Kind == "operation"
		}
		if important || level >= zerolog.Level(l.persistLevel.Load()) {
			if err := l.sink.Enqueue(p, important); err != nil {
				Emergency("log persistence failed", err)
			}
		}
	}
	return len(p), nil
}

func (l *Logger) Close(ctx context.Context) error {
	if l.sink == nil {
		return nil
	}
	return l.sink.Close(ctx)
}

func (l *Logger) Stats() Stats {
	if l == nil || l.sink == nil {
		return Stats{}
	}
	return l.sink.Stats()
}

var defaultLogger atomic.Pointer[Logger]

func init() {
	// Route arbitrary structured log values through Sonic as well, preserving
	// zerolog's unescaped HTML and newline-free field representation.
	zerolog.InterfaceMarshalFunc = sonic.Config{
		SortMapKeys:      true,
		CompactMarshaler: true,
		ValidateString:   true,
	}.Froze().Marshal
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFunc = func() time.Time { return time.Now().UTC() }
	SetDefault(New(Options{Level: "info", PersistLevel: "info", Format: "auto"}, nil))
}
func Default() *Logger                     { return defaultLogger.Load() }
func SetDefault(l *Logger)                 { defaultLogger.Store(l) }
func Component(name string) zerolog.Logger { return Default().Component(name) }
func FromContext(ctx context.Context) *zerolog.Logger {
	l := zerolog.Ctx(ctx)
	if l.GetLevel() == zerolog.Disabled {
		return &Default().logger
	}
	return l
}
func Debug() *zerolog.Event { return Default().Event(zerolog.DebugLevel) }
func Info() *zerolog.Event  { return Default().Event(zerolog.InfoLevel) }
func Warn() *zerolog.Event  { return Default().Event(zerolog.WarnLevel) }
func Error() *zerolog.Event { return Default().Event(zerolog.ErrorLevel) }

// Emergency bypasses the persistence writer to prevent recursive logging.
func Emergency(message string, err error) {
	l := zerolog.New(os.Stderr).With().Timestamp().Str("component", "logging").Logger()
	l.Error().Err(err).Msg(message)
}
