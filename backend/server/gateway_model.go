package server

import "github.com/elysia-api/backend/protocol"

// Client aliases replace valid model identities only. Replacing an invalid
// provider field would hide it from the final wire contract check.
func applyClientModelAlias(response *protocol.Response, alias protocol.Value) {
	var model string
	if response.Model.Decode(&model) == nil && model != "" {
		response.Model = alias
	}
}
