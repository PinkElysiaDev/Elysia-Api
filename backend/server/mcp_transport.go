package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRequestContextKey struct{}

// One native Hertz POST owns one SDK connection. SDK workers exchange messages
// through bounded channels; only the HTTP handler touches the Hertz context.
type mcpHTTPConnection struct {
	in   chan jsonrpc.Message
	out  chan jsonrpc.Message
	done chan struct{}
	once sync.Once
}

func (c *mcpHTTPConnection) Connect(context.Context) (mcp.Connection, error) { return c, nil }
func (c *mcpHTTPConnection) SupportsProtocolVersion(v string) bool {
	return v == mcpModernProtocolVersion
}
func (c *mcpHTTPConnection) SessionID() string { return "" }
func (c *mcpHTTPConnection) Close() error      { c.once.Do(func() { close(c.done) }); return nil }
func (c *mcpHTTPConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	case msg := <-c.in:
		return msg, nil
	}
}
func (c *mcpHTTPConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.ErrClosedPipe
	case c.out <- msg:
		return nil
	}
}

func (s *Server) serveMCPRequest(ctx context.Context, c *app.RequestContext) {
	if address := c.GetConn().LocalAddr(); address != nil && loopbackHost(address.String()) && !loopbackHost(string(c.Host())) {
		c.String(http.StatusForbidden, "invalid Host header")
		return
	}
	if !c.IsPost() {
		c.Header("Allow", "POST")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	contentType, _, err := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if err != nil || contentType != "application/json" {
		c.String(415, "Content-Type must be application/json")
		return
	}
	jsonOK, streamOK := false, false
	for _, value := range c.Request.Header.GetAll("Accept") {
		for _, token := range strings.Split(value, ",") {
			base, _, _ := strings.Cut(token, ";")
			switch strings.ToLower(strings.TrimSpace(base)) {
			case "application/json", "application/*":
				jsonOK = true
			case "text/event-stream", "text/*":
				streamOK = true
			case "*/*":
				jsonOK, streamOK = true, true
			}
		}
	}
	if !jsonOK || !streamOK {
		c.String(400, "Accept must include application/json and text/event-stream")
		return
	}
	body, err := protocol.ReadBoundedBody(requestBody(c), agentMaxBodyBytes)
	if err != nil {
		status := http.StatusBadRequest
		var limit *protocol.ResourceLimitError
		if errors.As(err, &limit) {
			status = http.StatusRequestEntityTooLarge
		}
		c.String(status, "%s", err)
		return
	}
	msg, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		c.Data(400, "application/json", []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"invalid JSON-RPC request"}}`))
		return
	}
	req, ok := msg.(*jsonrpc.Request)
	if !ok {
		c.Status(202)
		return
	}
	var params struct {
		Meta map[string]any `json:"_meta"`
		Name string         `json:"name"`
		URI  string         `json:"uri"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) != nil {
		c.String(400, "invalid params")
		return
	}
	version, _ := params.Meta[mcp.MetaKeyProtocolVersion].(string)
	if version == "" {
		writeMCPTransportError(c, req.ID, jsonrpc.CodeInvalidParams, "missing protocol version metadata", nil)
		return
	}
	if version != string(c.GetHeader("MCP-Protocol-Version")) {
		writeMCPTransportError(c, req.ID, mcp.CodeHeaderMismatch, "protocol version metadata must match the HTTP header", nil)
		return
	}
	if version != mcpModernProtocolVersion {
		data, _ := json.Marshal(mcp.UnsupportedProtocolVersionData{Supported: []string{mcpModernProtocolVersion}, Requested: version})
		writeMCPTransportError(c, req.ID, mcp.CodeUnsupportedProtocolVersion, "unsupported protocol version", data)
		return
	}
	if method := string(c.GetHeader("MCP-Method")); method != req.Method {
		writeMCPTransportError(c, req.ID, mcp.CodeHeaderMismatch, "MCP-Method does not match request", nil)
		return
	}
	if req.Method == "tools/call" || req.Method == "prompts/get" || req.Method == "resources/read" {
		name := params.Name
		if req.Method == "resources/read" {
			name = params.URI
		}
		if name == "" || string(c.GetHeader("MCP-Name")) != name {
			writeMCPTransportError(c, req.ID, mcp.CodeHeaderMismatch, "MCP-Name must match the request", nil)
			return
		}
	}
	if !req.ID.IsValid() {
		c.Status(202)
		return
	}
	conn := &mcpHTTPConnection{in: make(chan jsonrpc.Message, 1), out: make(chan jsonrpc.Message), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = context.WithValue(ctx, mcpRequestContextKey{}, ctx)
	session, err := s.mcpHandler.Connect(ctx, conn, nil)
	if err != nil {
		c.String(500, "%s", err)
		return
	}
	defer func() { cancel(); _ = conn.Close(); _ = session.Close() }()
	conn.in <- req
	writer := startSSE(c)
	for {
		select {
		case <-ctx.Done():
			return
		case <-conn.done:
			return
		case output := <-conn.out:
			encoded, err := jsonrpc.EncodeMessage(output)
			if err != nil {
				return
			}
			if writer.WriteEvent("", "message", encoded) != nil {
				return
			}
			if _, complete := output.(*jsonrpc.Response); complete {
				return
			}
		}
	}
}

func loopbackHost(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = strings.Trim(address, "[]")
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func writeMCPTransportError(c *app.RequestContext, id jsonrpc.ID, code int, message string, data json.RawMessage) {
	body, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: id, Error: &jsonrpc.Error{Code: int64(code), Message: message, Data: data}})
	if err != nil {
		c.String(500, "%s", err)
		return
	}
	c.Data(400, "application/json", body)
}
