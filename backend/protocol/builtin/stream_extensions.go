package builtin

import (
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

// Known event metadata is decoded by the wire codec. Everything else remains
// explicit evidence, including extensions on usage-only tail frames.
func (stream *streamModule) captureFrameExtensions(fields p.Object) (p.Value, error) {
	extra := p.Object{}
	capture := func(path string, object p.Object, known ...string) {
		if value := collectUnknown(object, known); !value.IsZero() {
			extra[path] = value
		}
	}
	switch stream.name {
	case Chat:
		capture("/", fields, "id", "model", "object", "choices", "usage", "error", "created")
		choices, err := readArray(fields["choices"])
		if err != nil {
			return p.Value{}, err
		}
		for index, value := range choices {
			choice, err := value.ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			path := fmt.Sprintf("/choices/%d", index)
			capture(path, choice, "index", "delta", "finish_reason")
			delta, err := nestedObject(choice, "delta")
			if err != nil {
				return p.Value{}, err
			}
			capture(path+"/delta", delta, "role", "content", "reasoning_content", "refusal", "tool_calls")
			calls, err := readArray(delta["tool_calls"])
			if err != nil {
				return p.Value{}, err
			}
			for index, value := range calls {
				call, err := value.ReadObject()
				if err != nil {
					return p.Value{}, err
				}
				path := fmt.Sprintf("%s/delta/tool_calls/%d", path, index)
				capture(path, call, "index", "id", "type", "function")
				function, err := nestedObject(call, "function")
				if err != nil {
					return p.Value{}, err
				}
				capture(path+"/function", function, "name", "arguments")
			}
		}
	case Anthropic:
		capture("/", fields, "type", "index", "message", "content_block", "delta", "usage", "error")
		if !fields["message"].IsZero() {
			message, err := fields["message"].ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			capture("/message", message, "id", "model", "type", "role", "content", "usage", "stop_reason", "stop_sequence")
		}
		if !fields["delta"].IsZero() {
			delta, err := fields["delta"].ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			capture("/delta", delta, "type", "text", "thinking", "partial_json", "signature", "stop_reason", "stop_sequence")
		}
	case Responses:
		if fields["type"] == p.StringValue("error") {
			capture("/", fields, "type", "sequence_number", "error", "message", "code", "param")
		} else {
			capture("/", fields, "type", "sequence_number", "response_id", "output_index", "content_index", "summary_index", "item_id", "delta", "text", "refusal", "arguments", "input", "item", "part", "response", "error", "logprobs", "annotation", "annotation_index")
		}
		if fields["type"] == p.StringValue("response.in_progress") {
			response, err := fields["response"].ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			if _, err := decodeResponsesToolUsage(response, nil); err != nil {
				return p.Value{}, err
			}
			usageExtra, err := stream.module.usageExtensions(response["usage"])
			if err != nil {
				return p.Value{}, err
			}
			if !usageExtra.IsZero() {
				extra["/response/usage"] = usageExtra
			}
			capture("/response", response, "id", "model", "status", "usage", "object", "created_at", "output", "error", "incomplete_details")
		}
		if value := fields["item"]; !value.IsZero() {
			item, err := value.ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			switch item["type"] {
			case p.StringValue("message"):
				// Message metadata was attached by the message decoder. Consume
				// the same validated fields here instead of duplicating them as opaque.
				if _, err := stream.module.extractMetadata(item, "message", "/item"); err != nil {
					return p.Value{}, err
				}
				capture("/item", item, "type", "id", "role", "status", "content", "phase")
				parts, err := readArray(item["content"])
				if err != nil {
					return p.Value{}, err
				}
				for index, value := range parts {
					part, err := value.ReadObject()
					if err != nil {
						return p.Value{}, err
					}
					capture(fmt.Sprintf("/item/content/%d", index), part, "type", "text", "refusal", "annotations", "logprobs")
				}
			case p.StringValue("function_call"):
				capture("/item", item, "type", "id", "call_id", "name", "arguments", "status")
			case p.StringValue("custom_tool_call"):
				capture("/item", item, "type", "id", "call_id", "name", "input", "status")
			}
		}
		if !fields["part"].IsZero() {
			part, err := fields["part"].ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			capture("/part", part, "type", "text", "refusal", "annotations", "logprobs")
		}
	case Gemini:
		capture("/", fields, "responseId", "modelVersion", "candidates", "usageMetadata", "error")
		candidates, err := readArray(fields["candidates"])
		if err != nil {
			return p.Value{}, err
		}
		for index, value := range candidates {
			candidate, err := value.ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			path := fmt.Sprintf("/candidates/%d", index)
			capture(path, candidate, "index", "content", "finishReason")
			content, err := nestedObject(candidate, "content")
			if err != nil {
				return p.Value{}, err
			}
			capture(path+"/content", content, "role", "parts")
		}
	}
	for _, entry := range []struct {
		path  string
		value p.Value
	}{{"/usage", fields["usage"]}, {"/usageMetadata", fields["usageMetadata"]}} {
		usage, err := stream.module.usageExtensions(entry.value)
		if err != nil {
			return p.Value{}, err
		}
		if !usage.IsZero() {
			extra[entry.path] = usage
		}
	}
	if value := fields["message"]; stream.name == Anthropic && !value.IsZero() {
		message, err := value.ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		usage, err := stream.module.usageExtensions(message["usage"])
		if err != nil {
			return p.Value{}, err
		}
		if !usage.IsZero() {
			extra["/message/usage"] = usage
		}
	}
	if len(extra) == 0 {
		return p.Value{}, nil
	}
	return object(extra), nil
}
