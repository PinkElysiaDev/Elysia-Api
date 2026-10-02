package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// SessionFrame carries exactly one WebSocket message. Payload is owned by the
// receiver until Write returns; implementations must enforce the read limit.
type SessionFrame struct {
	IsBinary bool
	Payload  []byte
}

// SessionConnection separates maintained WebSocket framing from the shared
// bounded session scheduler. Close must immediately unblock Read and Write.
type SessionConnection interface {
	Read(context.Context) (SessionFrame, error)
	Write(context.Context, SessionFrame) error
	Ping(context.Context) error
	Close() error
}

// SessionTranslator runs only in the coordinator goroutine and owns semantic
// association, model authorization, diagnostics and per-response accounting.
type SessionTranslator interface {
	Convert(context.Context, EventOrigin, SessionFrame) ([]SessionFrame, error)
	Finish() error
	IsClosed() bool
}

type sessionPacket struct {
	origin EventOrigin
	frame  SessionFrame
	end    error
}

type sessionQueue struct {
	mu                 sync.Mutex
	packets            []sessionPacket
	changed            chan struct{}
	items, bytes       int
	maxItems, maxBytes int
}

func newSessionQueue(items, bytes int) *sessionQueue {
	return &sessionQueue{changed: make(chan struct{}), maxItems: items, maxBytes: bytes}
}

func (queue *sessionQueue) signal() { close(queue.changed); queue.changed = make(chan struct{}) }

func (queue *sessionQueue) put(ctx context.Context, packet sessionPacket) error {
	size := len(packet.frame.Payload)
	if size > queue.maxBytes {
		return streamIssue(LimitExceeded, "/session/queueBytes", "frame exceeds queue byte budget")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		queue.mu.Lock()
		if queue.items < queue.maxItems && queue.bytes+size <= queue.maxBytes {
			queue.packets = append(queue.packets, packet)
			queue.items++
			queue.bytes += size
			queue.signal()
			queue.mu.Unlock()
			return nil
		}
		changed := queue.changed
		queue.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (queue *sessionQueue) take(ctx context.Context) (sessionPacket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return sessionPacket{}, err
		}
		queue.mu.Lock()
		if len(queue.packets) > 0 {
			packet := queue.packets[0]
			queue.packets[0] = sessionPacket{}
			queue.packets = queue.packets[1:]
			queue.mu.Unlock()
			return packet, nil
		}
		changed := queue.changed
		queue.mu.Unlock()
		select {
		case <-ctx.Done():
			return sessionPacket{}, ctx.Err()
		case <-changed:
		}
	}
}

func (queue *sessionQueue) release(packet sessionPacket) {
	queue.mu.Lock()
	queue.items--
	queue.bytes -= len(packet.frame.Payload)
	queue.signal()
	queue.mu.Unlock()
}

func (queue *sessionQueue) drain(ctx context.Context) error {
	for {
		queue.mu.Lock()
		isEmpty, changed := queue.items == 0, queue.changed
		queue.mu.Unlock()
		if isEmpty {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// RunSession forwards two independent lanes without reconnecting or replaying
// application messages. Queue budgets include writes in progress; at most one
// additional bounded frame per reader is retained while waiting for capacity.
// Cancellation always closes both connections and joins every worker.
func RunSession(ctx context.Context, client, upstream SessionConnection, clientConfig, upstreamConfig SessionConfig, translator SessionTranslator) (failure error) {
	ctx, cancel := context.WithCancelCause(ctx)
	var workers sync.WaitGroup
	incoming := newSessionQueue(min(clientConfig.QueueItems, upstreamConfig.QueueItems), min(clientConfig.QueueBytes, upstreamConfig.QueueBytes))
	clientOutput, upstreamOutput := newSessionQueue(clientConfig.QueueItems, clientConfig.QueueBytes), newSessionQueue(upstreamConfig.QueueItems, upstreamConfig.QueueBytes)
	defer func() {
		if ctx.Err() == nil {
			closeTimeout := time.Duration(min(clientConfig.CloseMillis, upstreamConfig.CloseMillis)) * time.Millisecond
			if failure != nil {
				sendSessionFailure(ctx, clientOutput, translator, failure, clientConfig)
			}
			timer := time.AfterFunc(closeTimeout, func() {
				cancel(context.DeadlineExceeded)
				// A peer may have already ended its reader. Close sockets as well
				// so the remaining close handshake cannot outlive this deadline.
				_ = client.Close()
				_ = upstream.Close()
			})
			var closing sync.WaitGroup
			for _, connection := range []SessionConnection{client, upstream} {
				if graceful, ok := connection.(interface{ FinishSession(error) error }); ok {
					closing.Add(1)
					go func() { defer closing.Done(); _ = graceful.FinishSession(failure) }()
				}
			}
			closing.Wait()
			timer.Stop()
		}
		cancel(context.Canceled)
		_ = client.Close()
		_ = upstream.Close()
		workers.Wait()
	}()
	start := func(work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := work(); err != nil {
				cancel(err)
			}
		}()
	}
	for _, endpoint := range []struct {
		origin     EventOrigin
		connection SessionConnection
		config     SessionConfig
		output     *sessionQueue
	}{{ClientEvent, client, clientConfig, clientOutput}, {UpstreamEvent, upstream, upstreamConfig, upstreamOutput}} {
		start(func() error {
			return readSessionFrames(ctx, endpoint.connection, endpoint.origin, endpoint.config, incoming)
		})
		start(func() error { return writeSessionFrames(ctx, endpoint.connection, endpoint.config, endpoint.output) })
		start(func() error { return pingSession(ctx, endpoint.connection, endpoint.config) })
	}
	for {
		packet, err := incoming.take(ctx)
		if err != nil {
			return context.Cause(ctx)
		}
		if packet.end != nil {
			incoming.release(packet)
			if !errors.Is(packet.end, io.EOF) {
				return packet.end
			}
			return finishSession(ctx, translator, clientOutput, upstreamOutput, min(clientConfig.IdleMillis, upstreamConfig.IdleMillis))
		}
		frames, err := translator.Convert(ctx, packet.origin, packet.frame)
		incoming.release(packet)
		if err != nil {
			return err
		}
		queue, config := clientOutput, clientConfig
		if packet.origin == ClientEvent {
			queue, config = upstreamOutput, upstreamConfig
		}
		for _, frame := range frames {
			if len(frame.Payload) > config.FrameBytes {
				return streamIssue(LimitExceeded, "/session/frameBytes", "encoded frame exceeds peer limit")
			}
			deadline, stop := context.WithTimeout(ctx, time.Duration(config.IdleMillis)*time.Millisecond)
			err := queue.put(deadline, sessionPacket{origin: packet.origin, frame: frame})
			stop()
			if err != nil {
				return fmt.Errorf("session output backpressure: %w", err)
			}
		}
		if translator.IsClosed() {
			return finishSession(ctx, translator, clientOutput, upstreamOutput, min(clientConfig.IdleMillis, upstreamConfig.IdleMillis))
		}
	}
}

func sendSessionFailure(ctx context.Context, queue *sessionQueue, translator SessionTranslator, failure error, config SessionConfig) {
	encoder, ok := translator.(interface {
		EncodeFailure(context.Context, error) (SessionFrame, error)
	})
	if !ok {
		return
	}
	deadline, stop := context.WithTimeout(ctx, time.Duration(config.CloseMillis)*time.Millisecond)
	defer stop()
	frame, err := encoder.EncodeFailure(deadline, failure)
	if err != nil || len(frame.Payload) > config.FrameBytes {
		return
	}
	if err := queue.put(deadline, sessionPacket{frame: frame}); err == nil {
		_ = queue.drain(deadline)
	}
}

func finishSession(ctx context.Context, translator SessionTranslator, clientOutput, upstreamOutput *sessionQueue, idleMillis int) error {
	if err := translator.Finish(); err != nil {
		return err
	}
	deadline, stop := context.WithTimeout(ctx, time.Duration(idleMillis)*time.Millisecond)
	defer stop()
	if err := clientOutput.drain(deadline); err != nil {
		return err
	}
	return upstreamOutput.drain(deadline)
}

func readSessionFrames(ctx context.Context, connection SessionConnection, origin EventOrigin, config SessionConfig, queue *sessionQueue) error {
	for {
		deadline, stop := context.WithTimeout(ctx, time.Duration(config.IdleMillis)*time.Millisecond)
		frame, err := connection.Read(deadline)
		stop()
		if err != nil {
			return queue.put(ctx, sessionPacket{origin: origin, end: err})
		}
		if len(frame.Payload) > config.FrameBytes {
			return queue.put(ctx, sessionPacket{origin: origin, end: streamIssue(LimitExceeded, "/session/frameBytes", "received frame exceeds configured limit")})
		}
		if err := queue.put(ctx, sessionPacket{origin: origin, frame: frame}); err != nil {
			return err
		}
	}
}

func writeSessionFrames(ctx context.Context, connection SessionConnection, config SessionConfig, queue *sessionQueue) error {
	for {
		packet, err := queue.take(ctx)
		if err != nil {
			return err
		}
		deadline, stop := context.WithTimeout(ctx, time.Duration(config.IdleMillis)*time.Millisecond)
		err = connection.Write(deadline, packet.frame)
		stop()
		queue.release(packet)
		if err != nil {
			return err
		}
	}
}

func pingSession(ctx context.Context, connection SessionConnection, config SessionConfig) error {
	ticker := time.NewTicker(time.Duration(config.PingMillis) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			deadline, stop := context.WithTimeout(ctx, time.Duration(config.CloseMillis)*time.Millisecond)
			err := connection.Ping(deadline)
			stop()
			if err != nil {
				return err
			}
		}
	}
}
