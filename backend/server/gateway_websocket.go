package server

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/elysia-api/backend/protocol"
	"github.com/hertz-contrib/websocket"
)

// websocketSession uses Hertz's native upgrade and message transport. A single
// reader and writer are owned by the protocol scheduler; control writes are safe
// concurrently with either. Cancellation interrupts I/O using the original
// connection's deadline; Hertz alone closes it after all workers have drained.
type websocketSession struct {
	conn          *websocket.Conn
	transport     network.Conn
	pingID        atomic.Uint64
	mu            sync.Mutex
	pongs         map[string]chan struct{}
	closeTimeout  time.Duration
	peerClosed    chan struct{}
	closed        chan struct{}
	peerCloseOnce sync.Once
	closeOnce     sync.Once
}

func newWebsocketSession(conn *websocket.Conn, transport network.Conn, config protocol.SessionConfig) *websocketSession {
	s := &websocketSession{conn: conn, transport: transport, pongs: make(map[string]chan struct{}), closeTimeout: time.Duration(config.CloseMillis) * time.Millisecond, peerClosed: make(chan struct{}), closed: make(chan struct{})}
	conn.SetReadLimit(int64(config.FrameBytes))
	conn.SetCloseHandler(func(code int, text string) error {
		// Match the native library's default acknowledgement behavior: a
		// previously sent close frame already satisfies our side of the handshake.
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(s.closeTimeout))
		s.peerCloseOnce.Do(func() { close(s.peerClosed) })
		return nil
	})
	conn.SetPongHandler(func(id string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if ch, ok := s.pongs[id]; ok {
			delete(s.pongs, id)
			close(ch)
		}
		return nil
	})
	return s
}

func (s *websocketSession) Read(ctx context.Context) (protocol.SessionFrame, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.transport.SetDeadline(time.Now()) })
	defer stop()
	kind, payload, err := s.conn.ReadMessage()
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		err = io.EOF
	}
	return protocol.SessionFrame{IsBinary: kind == websocket.BinaryMessage, Payload: payload}, err
}
func (s *websocketSession) Write(ctx context.Context, frame protocol.SessionFrame) error {
	stop := context.AfterFunc(ctx, func() { _ = s.transport.SetDeadline(time.Now()) })
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	kind := websocket.TextMessage
	if frame.IsBinary {
		kind = websocket.BinaryMessage
	}
	return s.conn.WriteMessage(kind, frame.Payload)
}
func (s *websocketSession) Ping(ctx context.Context) error {
	id := fmt.Sprint(s.pingID.Add(1))
	ch := make(chan struct{})
	s.mu.Lock()
	s.pongs[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pongs, id); s.mu.Unlock() }()
	deadline, _ := ctx.Deadline()
	if err := s.conn.WriteControl(websocket.PingMessage, []byte(id), deadline); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}
func (s *websocketSession) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return s.transport.SetDeadline(time.Now())
}
func (s *websocketSession) FinishSession(failure error) error {
	code, message := websocket.CloseNormalClosure, ""
	if failure != nil {
		code, message = websocket.CloseInternalServerErr, "protocol_session_error"
	}
	select {
	case <-s.peerClosed:
		return nil
	default:
	}
	if err := s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(s.closeTimeout)); err != nil {
		return err
	}
	timer := time.NewTimer(s.closeTimeout)
	defer timer.Stop()
	select {
	case <-s.peerClosed:
		return nil
	case <-s.closed:
		return io.ErrClosedPipe
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func readSessionHandshake(c *app.RequestContext, operation protocol.Operation) (protocol.Value, error) {
	h := protocol.SessionHandshake{Query: map[string][]string{}, Headers: map[string][]string{}}
	for _, name := range operation.Session.QueryFields {
		for _, value := range c.URI().QueryArgs().PeekAll(name) {
			h.Query[name] = append(h.Query[name], string(value))
		}
	}
	for _, name := range operation.Session.HeaderFields {
		if values := c.Request.Header.GetAll(name); len(values) > 0 {
			h.Headers[name] = values
		}
	}
	return protocol.EncodeValue(h)
}
