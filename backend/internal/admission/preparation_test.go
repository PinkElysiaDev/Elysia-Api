package admission

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestPreparationFIFOAndBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := New(Limits{Enabled: true, MaxConcurrent: 2, MaxQueued: 2, MaxReservedBodyBytes: 10, QueueTimeout: time.Hour})
		a, _ := l.Acquire(t.Context(), 6)
		b, _ := l.Acquire(t.Context(), 4)
		first, second := make(chan *Lease, 1), make(chan *Lease, 1)
		go func() {
			lease, err := l.Acquire(t.Context(), 6)
			if err != nil {
				t.Error(err)
			}
			first <- lease
		}()
		synctest.Wait()
		go func() {
			lease, err := l.Acquire(t.Context(), 1)
			if err != nil {
				t.Error(err)
			}
			second <- lease
		}()
		synctest.Wait()
		if _, err := l.Acquire(t.Context(), 1); !errors.Is(err, ErrFull) {
			t.Fatal("unbounded queue", err)
		}
		a.Release()
		synctest.Wait()
		c := <-first
		select {
		case <-second:
			t.Fatal("small waiter bypassed FIFO/reservation budget")
		default:
		}
		b.Release()
		d := <-second
		c.Release()
		d.Release()
		d.Release()
		stats := l.Snapshot()
		if stats.Active != 0 || stats.Queued != 0 || stats.ReservedBodyBytes != 0 || stats.PeakActive != 2 || stats.PeakReservedBodyBytes != 10 || stats.PeakQueued != 2 {
			t.Fatal(stats)
		}
		if _, err := l.Acquire(t.Context(), 11); !errors.Is(err, ErrCapacity) {
			t.Fatal("oversized reservation admitted", err)
		}
	})
}

func TestPreparationTimeoutCancelAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := New(Limits{Enabled: true, MaxConcurrent: 1, MaxQueued: 1, MaxReservedBodyBytes: 10, QueueTimeout: time.Second})
		hold, _ := l.Acquire(t.Context(), 10)
		done := make(chan error, 1)
		go func() { _, err := l.Acquire(t.Context(), 1); done <- err }()
		time.Sleep(2 * time.Second)
		if err := <-done; !errors.Is(err, ErrTimeout) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		go func() { _, err := l.Acquire(ctx, 1); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		go func() { _, err := l.Acquire(t.Context(), 1); done <- err }()
		synctest.Wait()
		l.Close()
		l.Close()
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		hold.Release()
		if _, err := l.Acquire(t.Context(), 1); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		stats := l.Snapshot()
		if stats.Active != 0 || stats.Queued != 0 || stats.ReservedBodyBytes != 0 || stats.TimedOut != 1 || stats.Canceled != 1 {
			t.Fatal(stats)
		}
	})
}

func TestPreparationGrantCancellationRaceReleases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := New(Limits{Enabled: true, MaxConcurrent: 1, MaxQueued: 1, MaxReservedBodyBytes: 10, QueueTimeout: time.Hour})
		for range 100 {
			hold, _ := l.Acquire(t.Context(), 10)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { lease, _ := l.Acquire(ctx, 10); lease.Release(); close(done) }()
			synctest.Wait()
			go hold.Release()
			go cancel()
			<-done
			synctest.Wait()
			if stats := l.Snapshot(); stats.Active != 0 || stats.Queued != 0 || stats.ReservedBodyBytes != 0 {
				t.Fatal(stats)
			}
		}
	})
}

func BenchmarkPreparationAdmission(b *testing.B) {
	l := New(Limits{Enabled: true, MaxConcurrent: 8, MaxQueued: 32, MaxReservedBodyBytes: 64 << 20, QueueTimeout: time.Second})
	b.ReportAllocs()
	for b.Loop() {
		lease, err := l.Acquire(b.Context(), 1024)
		if err != nil {
			b.Fatal(err)
		}
		lease.Release()
	}
}
