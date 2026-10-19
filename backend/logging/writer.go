package logging

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	QueueCapacity = 2048
	QueueBytes    = 16 << 20
	BatchSize     = 64
	FlushInterval = 100 * time.Millisecond
	WriteTimeout  = 5 * time.Second
)

var ErrClosed = errors.New("log writer is closed")

type Stats struct {
	Queued              int       `json:"queued"`
	RetainedBytes       int64     `json:"retainedBytes"`
	Written             uint64    `json:"written"`
	Dropped             uint64    `json:"dropped"`
	Failures            uint64    `json:"failures"`
	Synchronous         uint64    `json:"synchronous"`
	LastWriteAt         time.Time `json:"lastWriteAt"`
	LastBatchDurationMS int64     `json:"lastBatchDurationMs"`
	LastError           string    `json:"lastError,omitempty"`
	Closed              bool      `json:"closed"`
}

// BatchSink must commit the complete batch atomically before returning nil.
type BatchSink func(context.Context, [][]byte) error

type Writer struct {
	mu         sync.Mutex
	queue      chan []byte
	done       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	sink       BatchSink
	stats      Stats
	writeGate  chan struct{}
	fallbackWG sync.WaitGroup
	closeOnce  sync.Once
	closeDone  chan struct{}
	err        error
}

func NewWriter(sink BatchSink) *Writer {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{queue: make(chan []byte, QueueCapacity), done: make(chan struct{}), closeDone: make(chan struct{}), ctx: ctx, cancel: cancel, sink: sink, writeGate: make(chan struct{}, 1)}
	go w.run()
	return w
}

// Diagnostics shed load on saturation. Important events instead receive a
// bounded synchronous commit; never claim that an in-memory enqueue is durable.
func (w *Writer) Enqueue(p []byte, important bool) error {
	w.mu.Lock()
	if w.stats.Closed {
		w.mu.Unlock()
		return ErrClosed
	}
	if len(w.queue) < cap(w.queue) && int64(len(p)) <= QueueBytes-w.stats.RetainedBytes {
		copy := append([]byte(nil), p...)
		w.stats.RetainedBytes += int64(len(copy))
		w.queue <- copy
		w.mu.Unlock()
		return nil
	}
	if !important {
		w.stats.Dropped++
		w.mu.Unlock()
		return nil
	}
	w.stats.Synchronous++
	w.fallbackWG.Add(1)
	w.mu.Unlock()
	defer w.fallbackWG.Done()
	return w.write([][]byte{p})
}

func (w *Writer) run() {
	defer close(w.done)
	for first := range w.queue {
		batch := [][]byte{first}
		timer := time.NewTimer(FlushInterval)
	collect:
		for len(batch) < BatchSize {
			select {
			case p, ok := <-w.queue:
				if !ok {
					break collect
				}
				batch = append(batch, p)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		if err := w.write(batch); err != nil {
			Emergency("system log batch could not be committed", err)
		}
		w.mu.Lock()
		for _, p := range batch {
			w.stats.RetainedBytes -= int64(len(p))
		}
		w.mu.Unlock()
	}
}

func (w *Writer) write(batch [][]byte) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(w.ctx, WriteTimeout)
	defer cancel()
	var err error
	select {
	case w.writeGate <- struct{}{}:
		err = w.sink(ctx, batch)
		<-w.writeGate
	case <-ctx.Done():
		err = ctx.Err()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.LastBatchDurationMS = time.Since(started).Milliseconds()
	if err != nil {
		w.stats.Failures += uint64(len(batch))
		w.stats.LastError = err.Error()
		w.err = err // retain the last failure, not an unbounded error chain
	} else {
		w.stats.Written += uint64(len(batch))
		w.stats.LastWriteAt = time.Now().UTC()
	}
	return err
}

func (w *Writer) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.stats
	st.Queued = len(w.queue)
	return st
}

// Closing admission and registering synchronous writers use the same lock.
// The caller's deadline cancels in-flight SQLite operations, but we still join
// the worker before returning, so closing its database is always safe.
func (w *Writer) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.stats.Closed = true
		close(w.queue)
		w.mu.Unlock()
		go func() { <-w.done; w.fallbackWG.Wait(); close(w.closeDone) }()
	})
	select {
	case <-w.closeDone:
	case <-ctx.Done():
		w.cancel()
		<-w.closeDone
	}
	w.cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.err, ctx.Err())
}
