package relay

import (
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// NewProtocolCompiler installs wire adapters that operate on the ordered model
// directly. The compiler owns neither HTTP clients nor credentials.
func NewProtocolCompiler(limits protocol.Limits) (*protocol.Compiler, error) {
	return protocol.NewCompiler(limits, builtin.Modules(), []string{"transport.websocket", "tasks.async"})
}
