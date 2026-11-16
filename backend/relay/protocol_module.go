package relay

import (
	"github.com/elysia-api/backend/protocol"
)

// NewProtocolCompiler enables the host transports for declarative wire/core mappings.
// The compiler owns neither HTTP clients nor credentials.
func NewProtocolCompiler(limits protocol.Limits) (*protocol.Compiler, error) {
	return protocol.NewCompiler(limits, []string{"transport.websocket", "tasks.async"})
}
