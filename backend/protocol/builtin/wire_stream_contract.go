package builtin

import (
	"fmt"
	"strconv"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

// This state belongs only to final wire validation. It does not rewrite native
// frames, infer missing roles, or change the permissive semantic decoder used
// when a foreign target can express a complete response from partial input.
type wireStreamContract struct {
	identity   [3]string // response ID, model, creation time
	bound      [3]bool
	bytes      int
	started    bool
	terminal   bool
	assistant  bool
	stopReason bool
	openBlocks map[int]bool
}

func wireStreamIssue(path, reason string) error {
	return p.IssuesError([]p.ConversionIssue{{Code: p.ConversionRejected, Severity: p.SeverityError, Stage: "wire.output", Path: path, Reason: reason}})
}

func (s *wireStreamContract) bind(index int, path string, value p.Value, limit int) error {
	if value.IsZero() {
		return nil
	}
	var normalized string
	var err error
	if index == 2 {
		var n int64
		err = value.Decode(&n)
		normalized = strconv.FormatInt(n, 10)
		if value.IsNull() || n < 0 {
			err = fmt.Errorf("invalid timestamp")
		}
	} else {
		normalized, err = stringValue(value)
		if normalized == "" {
			err = fmt.Errorf("empty identity")
		}
	}
	if err != nil {
		return wireStreamIssue(path, "stream identity has an invalid type or value")
	}
	if s.bound[index] {
		if s.identity[index] != normalized {
			return wireStreamIssue(path, "stream response identity changed after it was established")
		}
		return nil
	}
	if len(normalized) > limit-s.bytes {
		return wireStreamIssue(path, "stream identity exceeds the state buffer limit")
	}
	s.identity[index], s.bound[index] = normalized, true
	s.bytes += len(normalized)
	return nil
}

func (stream *streamModule) ValidateWireEvent(frame p.Value) error {
	fields, err := frame.ReadObject()
	if err != nil {
		return err
	}
	// Failures need neither an assistant role nor an earlier successful start.
	if fields["type"] == p.StringValue("error") || (!fields["error"].IsZero() && !fields["error"].IsNull()) {
		return nil
	}
	s := &stream.wire
	switch stream.name {
	case Chat:
		for i, key := range []string{"id", "model", "created"} {
			if err := s.bind(i, "/"+key, fields[key], stream.limits.BufferBytes); err != nil {
				return err
			}
		}
		choices, err := readArray(fields["choices"])
		if err != nil {
			return err
		}
		if s.terminal && len(choices) > 0 {
			return wireStreamIssue("/choices", "only empty-choice usage tails are allowed after Chat completion")
		}
		seen := map[int]bool{}
		for i, value := range choices {
			choice, err := value.ReadObject()
			if err != nil {
				return err
			}
			index, err := frameIndex(choice["index"])
			if err != nil {
				return err
			}
			base := fmt.Sprintf("/choices/%d", i)
			if seen[index] {
				return wireStreamIssue(base+"/index", "choice index occurs more than once in a frame")
			}
			seen[index] = true
			delta, err := choice["delta"].ReadObject()
			if err != nil {
				return err
			}
			if role := delta["role"]; !role.IsZero() {
				text, err := stringValue(role)
				if err != nil || text != "assistant" {
					return wireStreamIssue(base+"/delta/role", "Chat output role must be assistant")
				}
				s.assistant = true
			}
			if finish := choice["finish_reason"]; !finish.IsZero() && !finish.IsNull() {
				if !s.assistant {
					return wireStreamIssue(base+"/delta/role", "Chat completion has no assistant role in any delta")
				}
				s.terminal = true
			}
		}
	case Responses:
		kind, _ := stringValue(fields["type"])
		if kind == "response.created" {
			if s.started {
				return wireStreamIssue("/type", "Responses stream repeats response.created")
			}
			s.started = true
		} else if strings.HasPrefix(kind, "response.") && !s.started {
			return wireStreamIssue("/type", "Responses output requires an earlier response.created")
		}
		if response := fields["response"]; !response.IsZero() {
			object, err := response.ReadObject()
			if err != nil {
				return err
			}
			for i, key := range []string{"id", "model", "created_at"} {
				if err := s.bind(i, "/response/"+key, object[key], stream.limits.BufferBytes); err != nil {
					return err
				}
			}
			status, _ := stringValue(object["status"])
			want := map[string]string{"response.in_progress": "in_progress", "response.completed": "completed", "response.incomplete": "incomplete", "response.failed": "failed"}[kind]
			if (kind == "response.created" && status != "in_progress" && status != "queued") || (want != "" && status != want) {
				return wireStreamIssue("/response/status", "Responses event type and response status disagree")
			}
		}
		if err := s.bind(0, "/response_id", fields["response_id"], stream.limits.BufferBytes); err != nil {
			return err
		}
	case Anthropic:
		kind, _ := stringValue(fields["type"])
		if kind == "ping" {
			return nil
		}
		if kind == "message_start" {
			if s.started {
				return wireStreamIssue("/type", "Anthropic stream repeats message_start")
			}
			s.started = true
			return nil
		}
		if !s.started {
			return wireStreamIssue("/type", "Anthropic output requires an earlier message_start")
		}
		if s.terminal {
			return wireStreamIssue("/type", "Anthropic event arrived after message_stop")
		}
		if strings.HasPrefix(kind, "content_block_") {
			if s.stopReason {
				return wireStreamIssue("/type", "Anthropic content arrived after message_delta")
			}
			index, err := frameIndex(fields["index"])
			if err != nil {
				return err
			}
			if kind == "content_block_start" {
				if s.openBlocks[index] {
					return wireStreamIssue("/index", "Anthropic content block is already open")
				}
				if len(s.openBlocks) >= stream.limits.StateItems {
					return wireStreamIssue("/index", "Anthropic open block count exceeds the state limit")
				}
				if s.openBlocks == nil {
					s.openBlocks = map[int]bool{}
				}
				s.openBlocks[index] = true
			} else {
				if !s.openBlocks[index] {
					return wireStreamIssue("/index", "Anthropic content block is not open")
				}
				if kind == "content_block_stop" {
					delete(s.openBlocks, index)
				}
			}
		}
		if kind == "message_delta" {
			if len(s.openBlocks) != 0 {
				return wireStreamIssue("/type", "Anthropic message_delta requires all content blocks to stop")
			}
			s.stopReason = true
		}
		if kind == "message_stop" {
			s.terminal = true
		}
	case Gemini:
		for i, key := range []string{"responseId", "modelVersion"} {
			if err := s.bind(i, "/"+key, fields[key], stream.limits.BufferBytes); err != nil {
				return err
			}
		}
		candidates, err := readArray(fields["candidates"])
		if err != nil {
			return err
		}
		seen := map[int]bool{}
		for i, value := range candidates {
			candidate, err := value.ReadObject()
			if err != nil {
				return err
			}
			base := fmt.Sprintf("/candidates/%d", i)
			index, err := frameIndex(candidate["index"])
			if err != nil {
				return err
			}
			if seen[index] {
				return wireStreamIssue(base+"/index", "candidate index occurs more than once in a frame")
			}
			seen[index] = true
			if content := candidate["content"]; !content.IsZero() {
				object, err := content.ReadObject()
				if err != nil {
					return err
				}
				if role := object["role"]; !role.IsZero() {
					text, err := stringValue(role)
					if err != nil || text != "model" {
						return wireStreamIssue(base+"/content/role", "Gemini output role must be model")
					}
				}
			}
		}
	}
	return nil
}
