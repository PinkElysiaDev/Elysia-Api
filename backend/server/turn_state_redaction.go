package server

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

// A bounded capture can end inside a state value, including an escaped JSON
// string. In that case discard the remaining log fragment, never the wire data.
var partialTurnStateKey = regexp.MustCompile(`(?i)\\*"x-codex-turn-state\\*"\s*:`)

func redactUsageTurnState(text string) string {
	return redactTurnStateText(text, 0)
}

func redactTurnStateText(text string, depth int) string {
	if depth >= protocol.DefaultLimits().Depth {
		return "[REDACTED: nested log limit]"
	}
	if wire, err := protocol.ParseValue([]byte(text)); err == nil {
		var value any
		if wire.Decode(&value) == nil {
			if redactTurnStateValue(&value, depth+1) {
				encoded, _ := json.Marshal(value)
				return string(encoded)
			}
			return text
		}
	}
	if strings.Contains(text, "data:") {
		lines := strings.SplitAfter(text, "\n")
		hasDataLine := false
		for i, line := range lines {
			if payload, ok := strings.CutPrefix(line, "data:"); ok {
				hasDataLine = true
				ending := ""
				if strings.HasSuffix(payload, "\n") {
					payload, ending = strings.TrimSuffix(payload, "\n"), "\n"
				}
				lines[i] = "data:" + redactTurnStateText(payload, depth+1) + ending
			}
		}
		text = strings.Join(lines, "")
		if hasDataLine {
			return text
		}
	}
	if at := partialTurnStateKey.FindStringIndex(text); at != nil {
		return text[:at[0]] + `"x-codex-turn-state":"[REDACTED]"`
	}
	return text
}

func redactTurnStateValue(value *any, depth int) bool {
	isChanged := false
	switch node := (*value).(type) {
	case map[string]any:
		for key, child := range node {
			if strings.EqualFold(key, "x-codex-turn-state") {
				node[key], isChanged = "[REDACTED]", true
			} else if redactTurnStateValue(&child, depth) {
				node[key], isChanged = child, true
			}
		}
	case []any:
		for i := range node {
			isChanged = redactTurnStateValue(&node[i], depth) || isChanged
		}
	case string:
		redacted := redactTurnStateText(node, depth)
		if redacted != node {
			*value, isChanged = redacted, true
		}
	}
	return isChanged
}
