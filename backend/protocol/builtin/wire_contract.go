package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
	"strings"
)

// validateWireContract checks the actual emitted wire, including after mappings.
// It does not disallow optional vendor extensions or invent missing counters.
func (adapter module) validateWireContract(direction p.Direction, value p.Value) error {
	if direction != p.EncodeRequest && direction != p.EncodeResponse && direction != p.EncodeEvent {
		return nil
	}
	fail := func(at, reason string) error {
		return p.IssuesError([]p.ConversionIssue{{Code: p.ConversionRejected, Severity: p.SeverityError, Stage: "wire.output", Path: at, Reason: reason}})
	}
	obj := func(v p.Value, at string) (p.Object, error) {
		if !v.IsObject() {
			return nil, fail(at, "wire contract requires an object")
		}
		return v.ReadObject()
	}
	str := func(v p.Value, at string) error {
		var s string
		if v.IsNull() || v.Decode(&s) != nil || s == "" {
			return fail(at, "wire contract requires a nonempty string")
		}
		return nil
	}
	num := func(v p.Value, at string) error {
		var n int64
		if v.IsNull() || v.Decode(&n) != nil || n < 0 {
			return fail(at, "wire contract requires a nonnegative integer")
		}
		return nil
	}
	arr := func(v p.Value, at string) ([]p.Value, error) {
		var a []p.Value
		if v.IsZero() || v.IsNull() || v.Decode(&a) != nil {
			return nil, fail(at, "wire contract requires an array")
		}
		return a, nil
	}
	fields, err := obj(value, "")
	if err != nil {
		return err
	}
	if direction == p.EncodeRequest {
		if adapter.name != Gemini {
			if err := str(fields["model"], "/model"); err != nil {
				return err
			}
		}
		if adapter.name == Chat || adapter.name == Responses {
			key := "messages"
			if adapter.name == Responses {
				key = "input"
			}
			input := fields[key]
			var text string
			isText := adapter.name == Responses && !input.IsZero() && !input.IsNull() && input.Decode(&text) == nil
			if !isText && !(adapter.name == Responses && input.IsZero() && !fields["previous_response_id"].IsZero()) {
				messages, err := arr(input, "/"+key)
				if err != nil {
					return err
				}
				for i, v := range messages {
					base := fmt.Sprintf("/%s/%d", key, i)
					m, err := obj(v, base)
					if err != nil {
						return err
					}
					if adapter.name == Responses && !m["type"].IsZero() && m["type"] != p.StringValue("message") {
						continue
					}
					var role string
					_ = m["role"].Decode(&role)
					valid := role == "system" || role == "developer" || role == "user" || role == "assistant"
					if adapter.name == Chat {
						valid = valid || role == "tool" || role == "function"
					}
					if !valid {
						return fail(base+"/role", "invalid message role")
					}
					if adapter.name == Responses && role == "assistant" {
						var parts []p.Value
						if m["content"].Decode(&parts) == nil {
							for j, part := range parts {
								block, e := part.ReadObject()
								if e != nil {
									return fail(fmt.Sprintf("%s/content/%d", base, j), "expected content block")
								}
								if block["type"] == p.StringValue("input_text") {
									return fail(fmt.Sprintf("%s/content/%d/type", base, j), "assistant history requires output_text, not input_text")
								}
							}
						}
					}
				}
			}
		}
		if adapter.name == Anthropic {
			messages, e := arr(fields["messages"], "/messages")
			if e != nil {
				return e
			}
			for i, v := range messages {
				m, e := obj(v, fmt.Sprintf("/messages/%d", i))
				if e != nil {
					return e
				}
				if m["role"] != p.StringValue("user") && m["role"] != p.StringValue("assistant") {
					return fail(fmt.Sprintf("/messages/%d/role", i), "Anthropic messages only accept user or assistant; system belongs at top level")
				}
			}
		}
		if adapter.name == Gemini {
			messages, e := arr(fields["contents"], "/contents")
			if e != nil {
				return e
			}
			for i, v := range messages {
				m, e := obj(v, fmt.Sprintf("/contents/%d", i))
				if e != nil {
					return e
				}
				role := m["role"]
				if !role.IsZero() && role != p.StringValue("user") && role != p.StringValue("model") {
					return fail(fmt.Sprintf("/contents/%d/role", i), "Gemini contents only accept user or model")
				}
			}
		}
		// Decode the final bytes using this codec, not a user's after mapping.
		// Capability and resource scopes were checked earlier; repeat structural
		// associations here so a wire edit cannot create orphan tool results.
		request, err := adapter.decodeRequest(value, p.EvaluationContext{})
		if err != nil {
			return err
		}
		for _, issue := range p.CheckToolAssociations(request.Content, p.DefaultLimits()) {
			if issue.Code == p.InvalidInput || issue.Code == p.InvalidAssociation || issue.Code == p.LimitExceeded {
				issue.Stage = "wire.output"
				return p.IssuesError([]p.ConversionIssue{issue})
			}
		}
		return nil
	}
	if adapter.name == Responses && direction == p.EncodeEvent && fields["type"] == p.StringValue("error") {
		if err := num(fields["sequence_number"], "/sequence_number"); err != nil {
			return err
		}
		_, err := responsesFailurePayload(fields)
		return err
	}
	if !fields["error"].IsZero() && !fields["error"].IsNull() {
		_, err := obj(fields["error"], "/error")
		return err
	}
	if adapter.name == Chat {
		object := "chat.completion"
		if direction == p.EncodeEvent {
			object = "chat.completion.chunk"
		}
		if fields["object"] != p.StringValue(object) {
			return fail("/object", "unexpected Chat object type")
		}
		for _, k := range []string{"id", "model", "object"} {
			if e := str(fields[k], "/"+k); e != nil {
				return e
			}
		}
		if e := num(fields["created"], "/created"); e != nil {
			return e
		}
		choices, e := arr(fields["choices"], "/choices")
		if e != nil {
			return e
		}
		for i, v := range choices {
			base := fmt.Sprintf("/choices/%d", i)
			entry, e := obj(v, base)
			if e != nil {
				return e
			}
			if e = num(entry["index"], base+"/index"); e != nil {
				return e
			}
			key := "message"
			if direction == p.EncodeEvent {
				key = "delta"
			}
			m, e := obj(entry[key], base+"/"+key)
			if e != nil {
				return e
			}
			if direction == p.EncodeResponse && m["role"] != p.StringValue("assistant") {
				return fail(base+"/message/role", "Chat response requires assistant role")
			}
		}
	}
	if adapter.name == Responses {
		var item func(p.Value, string) error
		part := func(v p.Value, base string) error {
			f, e := obj(v, base)
			if e != nil {
				return e
			}
			if f["type"] == p.StringValue("output_text") || f["type"] == p.StringValue("reasoning_text") || f["type"] == p.StringValue("summary_text") {
				var text string
				if f["text"].IsNull() || f["text"].Decode(&text) != nil {
					return fail(base+"/text", "text part requires a string")
				}
				if f["type"] != p.StringValue("output_text") {
					return nil
				}
				_, e = arr(f["annotations"], base+"/annotations")
				return e
			}
			return nil
		}
		item = func(v p.Value, base string) error {
			f, e := obj(v, base)
			if e != nil {
				return e
			}
			if e = str(f["id"], base+"/id"); e != nil {
				return e
			}
			if f["type"] == p.StringValue("reasoning") {
				for _, field := range []string{"summary", "content"} {
					if field == "content" && f[field].IsZero() {
						continue
					}
					parts, err := arr(f[field], base+"/"+field)
					if err != nil {
						return err
					}
					for i, value := range parts {
						at := fmt.Sprintf("%s/%s/%d", base, field, i)
						entry, err := obj(value, at)
						if err != nil {
							return err
						}
						kind := "summary_text"
						if field == "content" {
							kind = "reasoning_text"
						}
						if entry["type"] != p.StringValue(kind) {
							return fail(at+"/type", "reasoning part has the wrong text representation")
						}
						if err := part(value, at); err != nil {
							return err
						}
					}
				}
			}
			if f["type"] == p.StringValue("message") {
				if phase := f["phase"]; !phase.IsZero() {
					if err := p.ValidateMetadataValue(Responses, "message", "phase", phase, base+"/phase"); err != nil {
						return fail(base+"/phase", "message phase must be commentary, final_answer or null")
					}
				}
				if f["role"] != p.StringValue("assistant") {
					return fail(base+"/role", "Responses output message requires assistant role")
				}
				if e = str(f["status"], base+"/status"); e != nil {
					return e
				}
				a, e := arr(f["content"], base+"/content")
				if e != nil {
					return e
				}
				for i, v := range a {
					if e = part(v, fmt.Sprintf("%s/content/%d", base, i)); e != nil {
						return e
					}
				}
			}
			return nil
		}
		response := func(v p.Value, base string) error {
			f, e := obj(v, base)
			if e != nil {
				return e
			}
			if f["object"] != p.StringValue("response") {
				return fail(base+"/object", "unexpected Responses object type")
			}
			for _, k := range []string{"id", "object", "model", "status"} {
				if e = str(f[k], base+"/"+k); e != nil {
					return e
				}
			}
			if e = num(f["created_at"], base+"/created_at"); e != nil {
				return e
			}
			a, e := arr(f["output"], base+"/output")
			if e != nil {
				return e
			}
			for i, v := range a {
				if e = item(v, fmt.Sprintf("%s/output/%d", base, i)); e != nil {
					return e
				}
			}
			return nil
		}
		if direction == p.EncodeResponse {
			return response(value, "")
		}
		var kind string
		if e := str(fields["type"], "/type"); e != nil {
			return e
		}
		_ = fields["type"].Decode(&kind)
		if kind == "error" {
			return nil
		}
		if strings.HasPrefix(kind, "response.") {
			if e := num(fields["sequence_number"], "/sequence_number"); e != nil {
				return e
			}
			if strings.HasPrefix(kind, "response.reasoning_text.") {
				if e := num(fields["content_index"], "/content_index"); e != nil {
					return e
				}
				field := "text"
				if kind == "response.reasoning_text.delta" {
					field = "delta"
				}
				if _, e := stringValue(fields[field]); e != nil {
					return fail("/"+field, "reasoning text event requires a string")
				}
			}
			if strings.HasPrefix(kind, "response.content_part.") || strings.HasPrefix(kind, "response.output_text.") || strings.HasPrefix(kind, "response.refusal.") || strings.HasPrefix(kind, "response.function_call_arguments.") || strings.HasPrefix(kind, "response.custom_tool_call_input.") || strings.HasPrefix(kind, "response.reasoning_summary_") || strings.HasPrefix(kind, "response.reasoning_text.") {
				if e := str(fields["item_id"], "/item_id"); e != nil {
					return e
				}
				if e := num(fields["output_index"], "/output_index"); e != nil {
					return e
				}
			}
			if !fields["response"].IsZero() {
				return response(fields["response"], "/response")
			}
			if !fields["item"].IsZero() {
				return item(fields["item"], "/item")
			}
			if !fields["part"].IsZero() {
				return part(fields["part"], "/part")
			}
		}
	}
	if adapter.name == Gemini {
		if candidates := fields["candidates"]; !candidates.IsZero() {
			a, e := arr(candidates, "/candidates")
			if e != nil {
				return e
			}
			for i, v := range a {
				entry, e := obj(v, fmt.Sprintf("/candidates/%d", i))
				if e != nil {
					return e
				}
				if content := entry["content"]; !content.IsZero() {
					m, e := obj(content, fmt.Sprintf("/candidates/%d/content", i))
					if e != nil {
						return e
					}
					if _, e = arr(m["parts"], fmt.Sprintf("/candidates/%d/content/parts", i)); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
