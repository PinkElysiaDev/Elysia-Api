package relay

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
)

type closeObservedConnection struct {
	net.Conn
	observed chan struct{}
	once     sync.Once
}

func (connection *closeObservedConnection) Write(raw []byte) (int, error) {
	count, err := connection.Conn.Write(raw)
	if count > 0 && raw[0] == 0x88 {
		connection.once.Do(func() { close(connection.observed) })
	}
	return count, err
}

type closeObservedWriter struct {
	http.ResponseWriter
	observed chan struct{}
}

func (writer closeObservedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffer, err := http.NewResponseController(writer.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	if err := buffer.Writer.Flush(); err != nil {
		_ = connection.Close()
		return nil, nil, err
	}
	observed := &closeObservedConnection{Conn: connection, observed: writer.observed}
	buffer.Writer.Reset(observed)
	return observed, buffer, nil
}

func TestWebSocketForceCloseInterruptsActiveHandshakeWithoutReader(t *testing.T) {
	observed := make(chan struct{})
	accepted := make(chan *WebSocketSession, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := AcceptProtocolSession(closeObservedWriter{w, observed}, r, protocol.DefaultSessionConfig())
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- connection
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.Dial(t.Context(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.CloseNow() })
	connection := <-accepted
	t.Cleanup(func() { _ = connection.Close() })
	graceful, forced := make(chan struct{}), make(chan struct{})
	go func() { _ = connection.FinishSession(nil); close(graceful) }()
	// The control-frame write establishes that graceful close owns the library
	// state. There is no reader whose context cancellation could close the socket.
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("close frame not written")
	}
	go func() { _ = connection.Close(); close(forced) }()
	for _, done := range []chan struct{}{forced, graceful} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("force close waited for the peer handshake")
		}
	}
}
