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
	fields, err := readWireObject(value, "")
	if err != nil {
		return err
	}
	if direction == p.EncodeRequest {
		return adapter.validateRequestWireContract(value, fields)
	}
	if adapter.name == Responses && direction == p.EncodeEvent && fields["type"] == p.StringValue("error") {
		if err := requireWireInteger(fields["sequence_number"], "/sequence_number"); err != nil {
			return err
		}
		_, err := responsesFailurePayload(fields)
		return err
	}
	if !fields["error"].IsZero() && !fields["error"].IsNull() {
		_, err := readWireObject(fields["error"], "/error")
		return err
	}
	if adapter.name == Chat {
		return adapter.validateChatWireContract(direction, fields)
	}
	if adapter.name == Responses {
		return adapter.validateResponsesWireContract(direction, value, fields)
	}
	if adapter.name == Gemini {
		return adapter.validateGeminiWireContract(direction, fields)
	}
	return nil
}

func wireContractError(at, reason string) error {
	return p.IssuesError([]p.ConversionIssue{{Code: p.ConversionRejected, Severity: p.SeverityError, Stage: "wire.output", Path: at, Reason: reason}})
}

func readWireObject(v p.Value, at string) (p.Object, error) {
	if !v.IsObject() {
		return nil, wireContractError(at, "wire contract requires an object")
	}
	return v.ReadObject()
}

func requireWireString(v p.Value, at string) error {
	var s string
	if v.IsNull() || v.Decode(&s) != nil || s == "" {
		return wireContractError(at, "wire contract requires a nonempty string")
	}
	return nil
}

func requireWireInteger(v p.Value, at string) error {
	var n int64
	if v.IsNull() || v.Decode(&n) != nil || n < 0 {
		return wireContractError(at, "wire contract requires a nonnegative integer")
	}
	return nil
}

func readWireArray(v p.Value, at string) ([]p.Value, error) {
	var a []p.Value
	if v.IsZero() || v.IsNull() || v.Decode(&a) != nil {
		return nil, wireContractError(at, "wire contract requires an array")
	}
	return a, nil
}

func (adapter module) validateRequestWireContract(value p.Value, fields p.Object) error {
	if adapter.name != Gemini {
		if err := requireWireString(fields["model"], "/model"); err != nil {
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
			messages, err := readWireArray(input, "/"+key)
			if err != nil {
				return err
			}
			if adapter.name == Chat {
				if err := validateChatToolSequence(messages); err != nil {
					return err
				}
			}
			for i, v := range messages {
				base := fmt.Sprintf("/%s/%d", key, i)
				m, err := readWireObject(v, base)
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
					return wireContractError(base+"/role", "invalid message role")
				}
				if adapter.name == Chat && role == "assistant" && !m["reasoning_content"].IsZero() && !m["reasoning_content"].IsNull() && (m["content"].IsZero() || m["content"].IsNull()) {
					calls, _ := readToolCalls(m["tool_calls"])
					if len(calls) == 0 && (m["refusal"].IsZero() || m["refusal"].IsNull()) {
						return wireContractError(base+"/content", "Chat thinking history needs associated assistant content or tool calls; enable chat_history")
					}
				}
				if adapter.name == Responses && role == "assistant" {
					var parts []p.Value
					if m["content"].Decode(&parts) == nil {
						for j, part := range parts {
							block, e := part.ReadObject()
							if e != nil {
								return wireContractError(fmt.Sprintf("%s/content/%d", base, j), "expected content block")
							}
							if block["type"] == p.StringValue("input_text") {
								return wireContractError(fmt.Sprintf("%s/content/%d/type", base, j), "assistant history requires output_text, not input_text")
							}
						}
					}
				}
			}
		}
	}
	if adapter.name == Anthropic {
		messages, e := readWireArray(fields["messages"], "/messages")
		if e != nil {
			return e
		}
		for i, v := range messages {
			m, e := readWireObject(v, fmt.Sprintf("/messages/%d", i))
			if e != nil {
				return e
			}
			if m["role"] != p.StringValue("user") && m["role"] != p.StringValue("assistant") {
				return wireContractError(fmt.Sprintf("/messages/%d/role", i), "Anthropic messages only accept user or assistant; system belongs at top level")
			}
		}
	}
	if adapter.name == Gemini {
		messages, e := readWireArray(fields["contents"], "/contents")
		if e != nil {
			return e
		}
		for i, v := range messages {
			m, e := readWireObject(v, fmt.Sprintf("/contents/%d", i))
			if e != nil {
				return e
			}
			role := m["role"]
			if !role.IsZero() && role != p.StringValue("user") && role != p.StringValue("model") {
				return wireContractError(fmt.Sprintf("/contents/%d/role", i), "Gemini contents only accept user or model")
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

func (adapter module) validateChatWireContract(direction p.Direction, fields p.Object) error {
	object := "chat.completion"
	if direction == p.EncodeEvent {
		object = "chat.completion.chunk"
	}
	if fields["object"] != p.StringValue(object) {
		return wireContractError("/object", "unexpected Chat object type")
	}
	for _, k := range []string{"id", "model", "object"} {
		if e := requireWireString(fields[k], "/"+k); e != nil {
			return e
		}
	}
	if e := requireWireInteger(fields["created"], "/created"); e != nil {
		return e
	}
	choices, e := readWireArray(fields["choices"], "/choices")
	if e != nil {
		return e
	}
	for i, v := range choices {
		base := fmt.Sprintf("/choices/%d", i)
		entry, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		if e = requireWireInteger(entry["index"], base+"/index"); e != nil {
			return e
		}
		key := "message"
		if direction == p.EncodeEvent {
			key = "delta"
		}
		m, e := readWireObject(entry[key], base+"/"+key)
		if e != nil {
			return e
		}
		if direction == p.EncodeResponse && m["role"] != p.StringValue("assistant") {
			return wireContractError(base+"/message/role", "Chat response requires assistant role")
		}
	}
	return nil
}

func (adapter module) validateResponsesWireContract(direction p.Direction, value p.Value, fields p.Object) error {
	var item func(p.Value, string) error
	part := func(v p.Value, base string) error {
		f, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		if f["type"] == p.StringValue("output_text") || f["type"] == p.StringValue("reasoning_text") || f["type"] == p.StringValue("summary_text") {
			var text string
			if f["text"].IsNull() || f["text"].Decode(&text) != nil {
				return wireContractError(base+"/text", "text part requires a string")
			}
			if f["type"] != p.StringValue("output_text") {
				return nil
			}
			_, e = readWireArray(f["annotations"], base+"/annotations")
			return e
		}
		return nil
	}
	item = func(v p.Value, base string) error {
		f, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		if e = requireWireString(f["id"], base+"/id"); e != nil {
			return e
		}
		if f["type"] == p.StringValue("reasoning") {
			for _, field := range []string{"summary", "content"} {
				if field == "content" && f[field].IsZero() {
					continue
				}
				parts, err := readWireArray(f[field], base+"/"+field)
				if err != nil {
					return err
				}
				for i, value := range parts {
					at := fmt.Sprintf("%s/%s/%d", base, field, i)
					entry, err := readWireObject(value, at)
					if err != nil {
						return err
					}
					kind := "summary_text"
					if field == "content" {
						kind = "reasoning_text"
					}
					if entry["type"] != p.StringValue(kind) {
						return wireContractError(at+"/type", "reasoning part has the wrong text representation")
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
					return wireContractError(base+"/phase", "message phase must be commentary, final_answer or null")
				}
			}
			if f["role"] != p.StringValue("assistant") {
				return wireContractError(base+"/role", "Responses output message requires assistant role")
			}
			if e = requireWireString(f["status"], base+"/status"); e != nil {
				return e
			}
			a, e := readWireArray(f["content"], base+"/content")
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
		f, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		if f["object"] != p.StringValue("response") {
			return wireContractError(base+"/object", "unexpected Responses object type")
		}
		for _, k := range []string{"id", "object", "model", "status"} {
			if e = requireWireString(f[k], base+"/"+k); e != nil {
				return e
			}
		}
		if e = requireWireInteger(f["created_at"], base+"/created_at"); e != nil {
			return e
		}
		a, e := readWireArray(f["output"], base+"/output")
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
	if e := requireWireString(fields["type"], "/type"); e != nil {
		return e
	}
	_ = fields["type"].Decode(&kind)
	if kind == "error" {
		return nil
	}
	if strings.HasPrefix(kind, "response.") {
		if e := requireWireInteger(fields["sequence_number"], "/sequence_number"); e != nil {
			return e
		}
		if strings.HasPrefix(kind, "response.reasoning_text.") {
			if e := requireWireInteger(fields["content_index"], "/content_index"); e != nil {
				return e
			}
			field := "text"
			if kind == "response.reasoning_text.delta" {
				field = "delta"
			}
			if _, e := stringValue(fields[field]); e != nil {
				return wireContractError("/"+field, "reasoning text event requires a string")
			}
		}
		if strings.HasPrefix(kind, "response.content_part.") || strings.HasPrefix(kind, "response.output_text.") || strings.HasPrefix(kind, "response.refusal.") || strings.HasPrefix(kind, "response.function_call_arguments.") || strings.HasPrefix(kind, "response.custom_tool_call_input.") || strings.HasPrefix(kind, "response.reasoning_summary_") || strings.HasPrefix(kind, "response.reasoning_text.") {
			if e := requireWireString(fields["item_id"], "/item_id"); e != nil {
				return e
			}
			if e := requireWireInteger(fields["output_index"], "/output_index"); e != nil {
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
	return nil
}

func (adapter module) validateGeminiWireContract(direction p.Direction, fields p.Object) error {
	if candidates := fields["candidates"]; !candidates.IsZero() {
		a, e := readWireArray(candidates, "/candidates")
		if e != nil {
			return e
		}
		for i, v := range a {
			entry, e := readWireObject(v, fmt.Sprintf("/candidates/%d", i))
			if e != nil {
				return e
			}
			if content := entry["content"]; !content.IsZero() {
				m, e := readWireObject(content, fmt.Sprintf("/candidates/%d/content", i))
				if e != nil {
					return e
				}
				parts, e := readWireArray(m["parts"], fmt.Sprintf("/candidates/%d/content/parts", i))
				if e != nil {
					return e
				}
				for j, value := range parts {
					path := fmt.Sprintf("/candidates/%d/content/parts/%d", i, j)
					part, e := readWireObject(value, path)
					if e != nil {
						return e
					}
					hasData := false
					if text := part["text"]; !text.IsZero() {
						var s string
						if text.IsNull() || text.Decode(&s) != nil {
							return wireContractError(path+"/text", "Gemini text must be a string, including explicit empty text")
						}
						hasData = true
					}
					for _, name := range []string{"inlineData", "fileData", "functionCall", "functionResponse", "executableCode", "codeExecutionResult"} {
						if data := part[name]; !data.IsZero() {
							if _, e := readWireObject(data, path+"/"+name); e != nil {
								return e
							}
							hasData = true
						}
					}
					if signature := part["thoughtSignature"]; !signature.IsZero() {
						if e := requireWireString(signature, path+"/thoughtSignature"); e != nil {
							return e
						}
						hasData = true // Continuation-only frames have separate association checks.
					}
					if !hasData {
						return wireContractError(path, "Gemini part requires data or a thought signature")
					}
				}
			}
		}
	}
	return nil
}
