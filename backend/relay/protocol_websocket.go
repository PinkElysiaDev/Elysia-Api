package relay

import (
	"context"
	"io"
	"net/http"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
)

// WebSocketSession adapts the maintained pure Go transport to the shared bounded
// scheduler. Application payloads are never transparently retried or transcoded.
type WebSocketSession struct{ connection *websocket.Conn }

func wrapWebSocket(connection *websocket.Conn, limit int) *WebSocketSession {
	connection.SetReadLimit(int64(limit))
	return &WebSocketSession{connection: connection}
}

// AcceptProtocolSession verifies the handshake and same-origin policy. Gateway
// authentication/model authorization must complete before calling this function.
func AcceptProtocolSession(writer http.ResponseWriter, request *http.Request, config protocol.SessionConfig) (*WebSocketSession, error) {
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	return wrapWebSocket(connection, config.FrameBytes), nil
}

// DialProtocolSession shares proxy/TLS/dial-time address policy with HTTP streams.
// Redirects are refused because a session credential must stay on its bound host.
func (adapter *OpenAIAdapter) DialProtocolSession(ctx context.Context, request *http.Request, config protocol.SessionConfig) (*WebSocketSession, error) {
	client := *adapter.streamClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	connection, _, err := websocket.Dial(ctx, request.URL.String(), &websocket.DialOptions{HTTPClient: &client, HTTPHeader: request.Header, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, sanitizeCustomTransportError(err)
	}
	return wrapWebSocket(connection, config.FrameBytes), nil
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
func (session *WebSocketSession) Close() error { return session.connection.CloseNow() }

// FinishSession sends an explicit protocol close code after queued frames drain.
// The scheduler bounds this handshake by cancelling the active reader context.
func (session *WebSocketSession) FinishSession(failure error) error {
	if failure != nil {
		return session.connection.Close(websocket.StatusInternalError, "protocol_session_error")
	}
	return session.connection.Close(websocket.StatusNormalClosure, "")
}
