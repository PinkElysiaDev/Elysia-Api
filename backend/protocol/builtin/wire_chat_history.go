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
		var fields [3]p.Value
		err := v.ReadObjectMembers([]string{"role", "tool_call_id", "tool_calls"}, fields[:])
		if err != nil {
			return err
		}
		at := fmt.Sprintf("/messages/%d", i)
		if len(pending) > 0 && !fields[0].IsString("tool") {
			return fail(at+"/role", "Chat requires results for all outstanding tool calls immediately after the assistant call message")
		}
		if fields[0].IsString("tool") {
			id := fields[1]
			if !pending[id] {
				return fail(at+"/tool_call_id", "tool result must match an outstanding call in the immediately preceding assistant turn")
			}
			delete(pending, id)
		}
		calls, err := readToolCalls(fields[2])
		if err != nil {
			return err
		}
		if len(calls) > 0 && !fields[0].IsString("assistant") {
			return fail(at+"/tool_calls", "only assistant messages may issue tool calls")
		}
		for j, v := range calls {
			var call [1]p.Value
			err := v.ReadObjectMembers([]string{"id"}, call[:])
			if err != nil {
				return err
			}
			id := call[0]
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
