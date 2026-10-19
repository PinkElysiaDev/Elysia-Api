package logging

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
)

func TestCloseDrainsConcurrentEventsAndOwnsBuffers(t *testing.T) {
	var received [][]byte
	w := NewWriter(func(_ context.Context, batch [][]byte) error {
		for _, p := range batch {
			received = append(received, append([]byte(nil), p...))
		}
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				p := []byte("original")
				if err := w.Enqueue(p, true); err != nil {
					t.Error(err)
				}
				copy(p, "mutated!")
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(received) != 512 {
		t.Fatalf("committed %d of 512", len(received))
	}
	for _, p := range received {
		if string(p) != "original" {
			t.Fatal("queued buffer was not owned")
		}
	}
	if st := w.Stats(); st.RetainedBytes != 0 || st.Queued != 0 || st.Written != 512 {
		t.Fatalf("stats: %+v", st)
	}
	if err := w.Enqueue([]byte("late"), true); !errors.Is(err, ErrClosed) {
		t.Fatalf("late enqueue: %v", err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSaturationDropsDiagnosticsButCommitsImportantEvents(t *testing.T) {
	for _, tc := range []struct {
		name        string
		payloadSize int
		queued      int
	}{
		{"records", 1, QueueCapacity},
		// The in-flight batch and one queued event together exhaust the budget.
		{"bytes", QueueBytes / 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var once sync.Once
				w := NewWriter(func(ctx context.Context, _ [][]byte) error {
					once.Do(func() { close(started) })
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				defer func() { unblock(); _ = w.Close(context.Background()) }()
				payload := make([]byte, tc.payloadSize)
				if err := w.Enqueue(payload, true); err != nil {
					t.Fatal(err)
				}
				<-started
				for i := 0; i < tc.queued+1; i++ {
					if err := w.Enqueue(payload, false); err != nil {
						t.Fatal(err)
					}
				}
				if st := w.Stats(); st.Dropped != 1 || st.Queued != tc.queued || st.RetainedBytes != int64((tc.queued+1)*tc.payloadSize) {
					t.Fatalf("saturation accounting: %+v", st)
				}
				done := make(chan error, 1)
				go func() { done <- w.Enqueue([]byte("operation"), true) }()
				// Keep the sink blocked until the important event is waiting for
				// the write gate. Scheduler timing cannot turn this into an enqueue.
				synctest.Wait()
				if st := w.Stats(); st.Synchronous != 1 {
					t.Fatalf("important event did not enter synchronous fallback: %+v", st)
				}
				unblock()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if err := w.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if st := w.Stats(); st.Written != uint64(tc.queued+2) || st.Failures != 0 || st.RetainedBytes != 0 {
					t.Fatalf("important event lost or buffers retained: %+v", st)
				}
			})
		})
	}
}

func TestCloseDeadlineCancelsAndJoinsSink(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	w := NewWriter(func(ctx context.Context, _ [][]byte) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	_ = w.Enqueue([]byte("event"), true)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned while sink still owns storage")
	}
	if w.Stats().Failures != 1 {
		t.Fatal("missing failure accounting")
	}
}

func TestIndependentThresholdsAndHotReload(t *testing.T) {
	var persisted [][]byte
	w := NewWriter(func(_ context.Context, batch [][]byte) error {
		persisted = append(persisted, batch...)
		return nil
	})
	l := New(Options{Level: "error", PersistLevel: "warn", Format: "json"}, w)
	var console bytes.Buffer
	l.SetOutput(&console)
	logger := l.Component("test")
	for _, level := range []zerolog.Level{zerolog.DebugLevel, zerolog.InfoLevel} {
		l.Event(level).Func(func(*zerolog.Event) {
			t.Fatal("filtered diagnostic built its fields")
		}).Msg("hidden")
	}
	logger.Info().Str("event_kind", "operation").Msg("operation")
	l.Event(zerolog.WarnLevel).Msg("persisted warning")
	if console.Len() != 0 {
		t.Fatalf("console threshold bypassed: %s", console.String())
	}
	l.Configure(Options{Level: "debug", PersistLevel: "error", Format: "json"})
	l.SetOutput(&console)
	logger.Debug().Msg("console debug")
	l.Event(zerolog.DebugLevel).Msg("hot-reloaded diagnostic")
	logger.Error().Msg("both")
	if err := l.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 3 {
		t.Fatalf("persisted %d events, want 3", len(persisted))
	}
	if !bytes.Contains(console.Bytes(), []byte("console debug")) {
		t.Fatal("existing logger did not observe new threshold")
	}
	if !bytes.Contains(console.Bytes(), []byte("hot-reloaded diagnostic")) {
		t.Fatal("diagnostic prefilter did not observe new threshold")
	}
}
