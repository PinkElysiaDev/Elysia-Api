package server

import "regexp"

var cliSecretFlagPattern = regexp.MustCompile(
	`(?i)(--(?:api-key|secret|new-secret|token)(?:=|\s+))("[^"]*"|'[^']*'|[^\s&;|]+)`)

// redactCLICommandLine masks credentials before a command is echoed or sent as
// progress. The CLI layer owns this presentation concern so it does not need
// to import the Agent package.
func redactCLICommandLine(command string) string {
	return cliSecretFlagPattern.ReplaceAllString(command, "${1}***")
}
