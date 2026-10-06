package builtin

import (
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) decodeChoice(value p.Value) (p.Value, error) {
	if value.IsZero() || value.IsNull() {
		return value, nil
	}
	if text, err := stringValue(value); err == nil {
		return object(p.Object{"mode": p.StringValue(text)}), nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	if adapter.name == Gemini {
		fields, err = fields["functionCallingConfig"].ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		mode, err := stringValue(fields["mode"])
		if err != nil {
			return p.Value{}, err
		}
		mode = strings.ToLower(mode)
		if mode == "any" {
			mode = "required"
		}
		return object(p.Object{"mode": p.StringValue(mode), "names": fields["allowedFunctionNames"]}), nil
	}
	typeName, err := optionalString(fields["type"])
	if err != nil {
		return p.Value{}, err
	}
	if adapter.name == Anthropic {
		mode := typeName
		if mode == "any" {
			mode = "required"
		}
		if mode == "tool" {
			mode = "function"
		}
		return object(p.Object{"mode": p.StringValue(mode), "name": fields["name"], "disableParallel": fields["disable_parallel_tool_use"]}), nil
	}
	if function := fields["function"]; !function.IsZero() {
		fields, err = function.ReadObject()
		if err != nil {
			return p.Value{}, err
		}
		typeName = "function"
	}
	if typeName == "" && !fields["name"].IsZero() {
		typeName = "function"
	}
	if typeName != "function" && typeName != "custom" {
		return p.Value{}, unsupported("/tool_choice", "unsupported native tool choice")
	}
	return object(p.Object{"mode": p.StringValue(typeName), "name": fields["name"]}), nil
}

func (adapter module) encodeChoice(value p.Value) (p.Value, error) {
	if value.IsZero() || value.IsNull() {
		return value, nil
	}
	fields, err := value.ReadObject()
	if err != nil {
		return p.Value{}, err
	}
	mode, err := stringValue(fields["mode"])
	if err != nil {
		return p.Value{}, err
	}
	if mode != "auto" && mode != "none" && mode != "required" && mode != "function" && mode != "custom" {
		return p.Value{}, fmt.Errorf("unsupported tool choice mode %q", mode)
	}
	if disable := fields["disableParallel"]; !disable.IsZero() && adapter.name != Anthropic {
		var enabled bool
		if err := disable.Decode(&enabled); err != nil || enabled {
			return p.Value{}, unsupported("/toolChoice/disableParallel", "target requires a separate parallel-tool policy")
		}
		// 显式 false 是无操作：忽略而不是拒绝（Claude Code 恒带该字段）。
	}
	if !fields["names"].IsZero() && adapter.name != Gemini {
		return p.Value{}, unsupported("/toolChoice/names", "target has no equivalent allowed-tool set")
	}
	switch adapter.name {
	case Chat, Responses:
		if mode == "auto" || mode == "none" || mode == "required" {
			return p.StringValue(mode), nil
		}
		if adapter.name == Chat {
			if mode == "custom" {
				return p.Value{}, unsupported("/toolChoice", "Chat cannot select a free-text tool")
			}
			return object(p.Object{"type": p.StringValue("function"), "function": object(p.Object{"name": fields["name"]})}), nil
		}
		return object(p.Object{"type": p.StringValue(mode), "name": fields["name"]}), nil
	case Anthropic:
		if mode == "custom" {
			return p.Value{}, unsupported("/toolChoice", "Anthropic cannot select a free-text tool")
		}
		if mode == "required" {
			mode = "any"
		}
		if mode == "function" {
			mode = "tool"
		}
		return object(p.Object{"type": p.StringValue(mode), "name": fields["name"], "disable_parallel_tool_use": fields["disableParallel"]}), nil
	case Gemini:
		if mode == "custom" {
			return p.Value{}, unsupported("/toolChoice", "Gemini cannot select a free-text tool")
		}
		names := fields["names"]
		if mode == "function" {
			mode = "required"
			names = array([]p.Value{fields["name"]})
		}
		if mode == "required" {
			mode = "any"
		}
		return object(p.Object{"functionCallingConfig": object(p.Object{"mode": p.StringValue(strings.ToUpper(mode)), "allowedFunctionNames": names})}), nil
	}
	return p.Value{}, fmt.Errorf("unsupported adapter")
}
