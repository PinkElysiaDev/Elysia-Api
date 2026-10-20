package server

import "github.com/elysia-api/backend/internal/clishell"

// redactCLICommandLine masks credentials before a command is echoed or sent as
// progress. The CLI layer owns this presentation concern so it does not need
// to import the Agent package.
func redactCLICommandLine(command string) string {
	return clishell.Redact(command)
}
