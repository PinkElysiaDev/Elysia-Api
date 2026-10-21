package relay

import (
	"bufio"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/elysia-api/backend/protocol"
)

// Standard-library fake providers exercise the outbound session transport.
// Production inbound WebSockets are upgraded natively by server/Hertz.
type sessionUpgradeWriter struct {
	http.ResponseWriter
	connection net.Conn
}

func (writer *sessionUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffer, err := http.NewResponseController(writer.ResponseWriter).Hijack()
	writer.connection = connection
	return connection, buffer, err
}

func acceptTestProtocolSession(writer http.ResponseWriter, request *http.Request, config protocol.SessionConfig) (*WebSocketSession, error) {
	upgrade := &sessionUpgradeWriter{ResponseWriter: writer}
	connection, err := websocket.Accept(upgrade, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	return wrapWebSocket(connection, upgrade.connection, config.FrameBytes), nil
}
