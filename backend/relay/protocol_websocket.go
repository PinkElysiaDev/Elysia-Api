package relay

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
)

// WebSocketSession adapts the maintained pure Go transport to the shared bounded
// scheduler. Application payloads are never transparently retried or transcoded.
type WebSocketSession struct {
	connection *websocket.Conn
	transport  io.Closer
}

func wrapWebSocket(connection *websocket.Conn, transport io.Closer, limit int) *WebSocketSession {
	connection.SetReadLimit(int64(limit))
	return &WebSocketSession{connection: connection, transport: transport}
}

type sessionUpgradeWriter struct {
	http.ResponseWriter
	connection net.Conn
}

func (writer *sessionUpgradeWriter) WriteHeaderNow() {
	if immediate, ok := writer.ResponseWriter.(interface{ WriteHeaderNow() }); ok {
		immediate.WriteHeaderNow()
	}
}

func (writer *sessionUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffer, err := http.NewResponseController(writer.ResponseWriter).Hijack()
	writer.connection = connection
	return connection, buffer, err
}

type sessionHandshakeTransport struct {
	http.RoundTripper
	body io.Closer
}

func (transport *sessionHandshakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.RoundTripper.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusSwitchingProtocols {
		transport.body = response.Body
	}
	return response, err
}

// AcceptProtocolSession verifies the handshake and same-origin policy. Gateway
// authentication/model authorization must complete before calling this function.
func AcceptProtocolSession(writer http.ResponseWriter, request *http.Request, config protocol.SessionConfig) (*WebSocketSession, error) {
	upgrade := &sessionUpgradeWriter{ResponseWriter: writer}
	connection, err := websocket.Accept(upgrade, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	return wrapWebSocket(connection, upgrade.connection, config.FrameBytes), nil
}

// DialProtocolSession shares proxy/TLS/dial-time address policy with HTTP streams.
// Redirects are refused because a session credential must stay on its bound host.
func (transport *ProtocolTransport) DialProtocolSession(ctx context.Context, request *http.Request, config protocol.SessionConfig) (*WebSocketSession, error) {
	client := *transport.streamClient
	handshake := &sessionHandshakeTransport{RoundTripper: client.Transport}
	client.Transport = handshake
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	connection, _, err := websocket.Dial(ctx, request.URL.String(), &websocket.DialOptions{HTTPClient: &client, HTTPHeader: request.Header, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, sanitizeTransportError(err)
	}
	return wrapWebSocket(connection, handshake.body, config.FrameBytes), nil
}

// Read returns one complete bounded data message; normal close becomes EOF and
// still requires the semantic replay to confirm a terminal/cancellation state.
func (session *WebSocketSession) Read(ctx context.Context) (protocol.SessionFrame, error) {
	kind, payload, err := session.connection.Read(ctx)
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return protocol.SessionFrame{}, io.EOF
	}
	return protocol.SessionFrame{IsBinary: kind == websocket.MessageBinary, Payload: payload}, err
}

// Write preserves message boundaries and binary bytes.
func (session *WebSocketSession) Write(ctx context.Context, frame protocol.SessionFrame) error {
	kind := websocket.MessageText
	if frame.IsBinary {
		kind = websocket.MessageBinary
	}
	return session.connection.Write(ctx, kind, frame.Payload)
}

// Ping waits for its correlated pong while the scheduler keeps Read active.
func (session *WebSocketSession) Ping(ctx context.Context) error { return session.connection.Ping(ctx) }

// Close immediately releases sockets during cancellation/failed handshakes.
func (session *WebSocketSession) Close() error {
	// CloseNow waits if a graceful close already owns the library's closing
	// state. Closing the underlying transport first interrupts that handshake
	// even after the scheduler's reader has exited.
	_ = session.transport.Close()
	return session.connection.CloseNow()
}

// FinishSession sends an explicit protocol close code after queued frames drain.
// The scheduler bounds this handshake by cancelling the active reader context.
func (session *WebSocketSession) FinishSession(failure error) error {
	if failure != nil {
		return session.connection.Close(websocket.StatusInternalError, "protocol_session_error")
	}
	return session.connection.Close(websocket.StatusNormalClosure, "")
}
