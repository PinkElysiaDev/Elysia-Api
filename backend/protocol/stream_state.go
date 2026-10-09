package protocol

import (
	"encoding/json"
	"strings"
)

type streamTool struct {
	callID string
	kind   InputKind
	input  strings.Builder
	text   TextTracker
	isDone bool
}

// StreamState owns bounded item association and append-only validation for a
// single response. Transport adapters decide when an upstream terminal occurs;
// usage is allowed after it, content is not.
type StreamState struct {
	limits     Limits
	tools      map[string]*streamTool
	callIDs    map[string]string
	texts      map[string]*TextTracker
	buffered   int
	isTerminal bool
}

// NewStreamState constructs response state with validated engine limits.
func NewStreamState(limits Limits) (*StreamState, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return createStreamState(limits), nil
}

// DefaultStreamState constructs state with the engine's resource defaults.
func DefaultStreamState() *StreamState { return createStreamState(DefaultLimits()) }

func createStreamState(limits Limits) *StreamState {
	return &StreamState{limits: limits, tools: make(map[string]*streamTool), callIDs: make(map[string]string), texts: make(map[string]*TextTracker)}
}

// CheckContent rejects content arriving after a response's terminal boundary.
func (state *StreamState) CheckContent() error {
	if state.isTerminal {
		return streamIssue(UpstreamContractViolation, "event", "content arrived after the response terminal")
	}
	return nil
}

// TrackText applies either a delta or a cumulative snapshot and returns the
// suffix to forward. Memory is bounded by item count, not generated text size.
func (state *StreamState) TrackText(key, text string, isSnapshot bool) (string, error) {
	if err := state.CheckContent(); err != nil {
		return "", err
	}
	tracker := state.texts[key]
	if tracker == nil {
		if err := state.checkItemLimit(); err != nil {
			return "", err
		}
		tracker = &TextTracker{}
		state.texts[key] = tracker
	}
	if isSnapshot {
		return tracker.Snapshot(text)
	}
	tracker.Append(text)
	return text, nil
}

// ObserveTool associates an item with its call identity and input type. Later
// frames may supply a previously absent ID but cannot change an established ID.
func (state *StreamState) ObserveTool(key, callID string, kind InputKind) error {
	if err := state.CheckContent(); err != nil {
		return err
	}
	if previous, exists := state.callIDs[callID]; callID != "" && exists && previous != key {
		return streamIssue(InvalidAssociation, key, "call ID is associated with multiple tool items")
	}
	tool := state.tools[key]
	if tool == nil {
		if err := state.checkItemLimit(); err != nil {
			return err
		}
		tool = &streamTool{kind: kind}
		state.tools[key] = tool
	}
	if tool.kind != kind || (tool.callID != "" && callID != "" && tool.callID != callID) {
		return streamIssue(InvalidAssociation, key, "tool identity or input type changed during a call")
	}
	if callID != "" {
		tool.callID = callID
		state.callIDs[callID] = key
	}
	return nil
}

// AppendTool records an input delta or complete snapshot. JSON arguments are
// validated only on completion; free-text input is never interpreted as JSON.
// Completion releases the retained input and keeps only its digest.
func (state *StreamState) AppendTool(key, input string, isSnapshot, isDone bool) error {
	tool := state.tools[key]
	if tool == nil {
		return streamIssue(InvalidAssociation, key, "tool input has no associated item")
	}
	if tool.isDone {
		if isSnapshot && isDone && len(input) == tool.text.Length() {
			_, err := tool.text.Snapshot(input)
			return err
		}
		return streamIssue(UpstreamContractViolation, key, "tool input arrived after completion")
	}
	if err := state.CheckContent(); err != nil {
		return err
	}
	delta := input
	if isSnapshot {
		var err error
		if tool.kind == JSONInput {
			delta, err = JSONToolSnapshotSuffix(tool.input.String(), input)
			if err == nil {
				tool.text.Append(delta)
			}
		} else {
			delta, err = tool.text.Snapshot(input)
		}
		if err != nil {
			return err
		}
	} else {
		tool.text.Append(delta)
	}
	if tool.kind == JSONInput {
		if state.buffered+len(delta) > state.limits.BufferBytes {
			return streamIssue(LimitExceeded, key, "tool input exceeds the response buffer limit")
		}
		tool.input.WriteString(delta)
		state.buffered += len(delta)
	}
	if isDone {
		return state.finishTool(key, tool)
	}
	return nil
}

// Finish validates pending inputs before accepting a terminal. Calling it
// again is idempotent, including streams with a separate end marker.
func (state *StreamState) Finish() error {
	if state.isTerminal {
		return nil
	}
	if err := state.ValidateComplete(); err != nil {
		return err
	}
	state.isTerminal = true
	return nil
}

// ValidateComplete validates and releases pending inputs before a renderer
// writes success, without preventing it from encoding remaining choice ends.
func (state *StreamState) ValidateComplete() error {
	for key, tool := range state.tools {
		if !tool.isDone {
			if err := state.finishTool(key, tool); err != nil {
				return err
			}
		}
	}
	return nil
}

func (state *StreamState) finishTool(key string, tool *streamTool) error {
	if tool.kind == JSONInput && !json.Valid([]byte(tool.input.String())) {
		return streamIssue(UpstreamContractViolation, key, "completed function arguments are missing or invalid JSON")
	}
	state.buffered -= tool.input.Len()
	tool.input.Reset()
	tool.isDone = true
	return nil
}

func (state *StreamState) checkItemLimit() error {
	if len(state.tools)+len(state.texts) >= state.limits.StateItems {
		return streamIssue(LimitExceeded, "items", "response exceeds the stream item limit")
	}
	return nil
}
