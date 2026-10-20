// Package admission owns bounded preparation reservations independently of HTTP,
// configuration storage, model quotas and upstream request lifetimes.
package admission

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrClosed   = errors.New("gateway preparation is shutting down")
	ErrFull     = errors.New("gateway preparation queue is full")
	ErrTimeout  = errors.New("gateway preparation queue deadline exceeded")
	ErrCapacity = errors.New("request body reservation exceeds gateway preparation capacity")
)

type Limits struct {
	Enabled                  bool
	MaxConcurrent, MaxQueued int
	MaxReservedBodyBytes     int64
	QueueTimeout             time.Duration
}

type Stats struct {
	Active, Queued, PeakActive, PeakQueued   int
	ReservedBodyBytes, PeakReservedBodyBytes int64
	Rejected, TimedOut, Canceled             uint64
}

// Limiter freezes limits at construction. Waiting is bounded and FIFO; a large
// head reservation cannot be starved by new smaller requests. Disabled limits
// retain diagnostics/close handling but deliberately do not bound admission.
type Limiter struct {
	mu      sync.Mutex
	limits  Limits
	closed  bool
	waiters list.List
	stats   Stats
}

type waiter struct {
	bytes int64
	ready chan struct{}
	lease *Lease
	err   error
}

type Lease struct {
	owner *Limiter
	bytes int64
	once  sync.Once
}

func New(limits Limits) *Limiter { return &Limiter{limits: limits} }

func (l *Limiter) fits(bytes int64) bool {
	return !l.limits.Enabled || l.stats.Active < l.limits.MaxConcurrent && bytes <= l.limits.MaxReservedBodyBytes-l.stats.ReservedBodyBytes
}

func (l *Limiter) grant(bytes int64) *Lease {
	l.stats.Active++
	l.stats.ReservedBodyBytes += bytes
	l.stats.PeakActive = max(l.stats.PeakActive, l.stats.Active)
	l.stats.PeakReservedBodyBytes = max(l.stats.PeakReservedBodyBytes, l.stats.ReservedBodyBytes)
	return &Lease{owner: l, bytes: bytes}
}

func (l *Limiter) Acquire(ctx context.Context, bytes int64) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bytes = max(1, bytes)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, ErrClosed
	}
	if l.limits.Enabled && bytes > l.limits.MaxReservedBodyBytes {
		l.stats.Rejected++
		l.mu.Unlock()
		return nil, ErrCapacity
	}
	if l.waiters.Len() == 0 && l.fits(bytes) {
		lease := l.grant(bytes)
		l.mu.Unlock()
		return lease, nil
	}
	if l.waiters.Len() >= l.limits.MaxQueued {
		l.stats.Rejected++
		l.mu.Unlock()
		return nil, ErrFull
	}
	w := &waiter{bytes: bytes, ready: make(chan struct{})}
	element := l.waiters.PushBack(w)
	l.stats.Queued = l.waiters.Len()
	l.stats.PeakQueued = max(l.stats.PeakQueued, l.stats.Queued)
	l.mu.Unlock()
	timer := time.NewTimer(l.limits.QueueTimeout)
	defer timer.Stop()
	var err error
	select {
	case <-w.ready:
		if w.err != nil {
			return nil, w.err
		}
		if err = ctx.Err(); err == nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if !closed {
				return w.lease, nil
			}
			err = ErrClosed
		}
		w.lease.Release()
		return nil, err
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = ErrTimeout
	}
	l.mu.Lock()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, ErrTimeout) {
		l.stats.TimedOut++
	} else {
		l.stats.Canceled++
	}
	if w.lease == nil && w.err == nil {
		l.waiters.Remove(element)
		l.stats.Queued = l.waiters.Len()
		l.drain()
	}
	lease := w.lease
	l.mu.Unlock()
	if lease != nil {
		lease.Release()
	}
	return nil, err
}

func (l *Limiter) drain() {
	for !l.closed && l.waiters.Len() > 0 {
		element := l.waiters.Front()
		w := element.Value.(*waiter)
		if !l.fits(w.bytes) {
			return
		}
		l.waiters.Remove(element)
		w.lease = l.grant(w.bytes)
		l.stats.Queued = l.waiters.Len()
		close(w.ready)
	}
}

func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		l := lease.owner
		l.mu.Lock()
		defer l.mu.Unlock()
		l.stats.Active--
		l.stats.ReservedBodyBytes -= lease.bytes
		l.drain()
	})
}

func (l *Limiter) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	for l.waiters.Len() > 0 {
		element := l.waiters.Front()
		w := element.Value.(*waiter)
		l.waiters.Remove(element)
		w.err = ErrClosed
		close(w.ready)
	}
	l.stats.Queued = 0
}

func (l *Limiter) Snapshot() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}
