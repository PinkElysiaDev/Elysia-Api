package server

import (
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/protocol"
)

// Extract HTTP metadata at ingress; continuation persistence and projection
// receive owned values and never retain a pooled Hertz request context.
func continuationSession(c *app.RequestContext, v protocol.Value) string {
	for _, name := range []string{"x-elysia-session-id", "x-claude-code-session-id", "claude-code-session-id", "x-session-id", "session_id"} {
		if value := strings.TrimSpace(string(c.GetHeader(name))); value != "" && len(value) <= 512 {
			return value
		}
	}
	reader := (protocol.EvaluationContext{}).WithValueReader(v)
	fields, _ := reader.ReadObject(v)
	metadata, _ := reader.ReadObject(fields["metadata"])
	var session string
	if metadata["session_id"].Decode(&session) == nil && session != "" && len(session) <= 512 {
		return session
	}
	var user string
	if metadata["user_id"].Decode(&user) == nil {
		if i := strings.LastIndex(user, "_session_"); i >= 0 {
			session = user[i+9:]
			if session != "" && len(session) <= 512 {
				return session
			}
		}
	}
	return ""
}
