package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
)

// validateChatToolSequence runs on final wire output, including native replay
// and after mappings. Generic association checks alone do not check adjacency.
func validateChatToolSequence(messages []p.Value) error {
	pending := map[p.Value]bool{}
	lastCall := ""
	fail := func(path, reason string) error {
		return p.IssuesError([]p.ConversionIssue{{Code: p.InvalidAssociation, Severity: p.SeverityError, Stage: "wire.output", Path: path, Reason: reason}})
	}
	for i, v := range messages {
		m, err := v.ReadObject()
		if err != nil {
			return err
		}
		at := fmt.Sprintf("/messages/%d", i)
		if len(pending) > 0 && m["role"] != p.StringValue("tool") {
			return fail(at+"/role", "Chat requires results for all outstanding tool calls immediately after the assistant call message")
		}
		if m["role"] == p.StringValue("tool") {
			id := m["tool_call_id"]
			if !pending[id] {
				return fail(at+"/tool_call_id", "tool result must match an outstanding call in the immediately preceding assistant turn")
			}
			delete(pending, id)
		}
		calls, err := readToolCalls(m["tool_calls"])
		if err != nil {
			return err
		}
		if len(calls) > 0 && m["role"] != p.StringValue("assistant") {
			return fail(at+"/tool_calls", "only assistant messages may issue tool calls")
		}
		for j, v := range calls {
			call, err := v.ReadObject()
			if err != nil {
				return err
			}
			id := call["id"]
			var text string
			if id.IsNull() || id.Decode(&text) != nil || text == "" || pending[id] {
				return fail(fmt.Sprintf("%s/tool_calls/%d/id", at, j), "tool call requires a unique nonempty ID")
			}
			pending[id], lastCall = true, at+"/tool_calls"
		}
	}
	if len(pending) > 0 {
		return fail(lastCall, "Chat generation requires results for every outstanding tool call; incomplete history is not discarded")
	}
	return nil
}
