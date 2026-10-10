package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) ValidateWireOutput(direction p.Direction, value p.Value) error {
	if err := adapter.validateWireContract(direction, value); err != nil {
		return err
	}
	if adapter.name != Anthropic || (direction != p.EncodeResponse && direction != p.EncodeEvent) {
		return nil
	}
	invalid := func(path, reason string) error {
		return p.IssuesError([]p.ConversionIssue{{Code: p.ConversionRejected, Severity: p.SeverityError, Stage: "wire.output", Path: path, Reason: reason}})
	}
	obj := func(v p.Value, path string) (p.Object, error) {
		if !v.IsObject() {
			return nil, invalid(path, "Anthropic output requires an object")
		}
		return v.ReadObject()
	}
	text := func(v p.Value, path string) error {
		var s string
		if v.IsNull() || v.Decode(&s) != nil || s == "" {
			return invalid(path, "Anthropic output requires a nonempty string")
		}
		return nil
	}
	number := func(v p.Value, path string) error {
		var n int64
		if v.IsNull() || v.Decode(&n) != nil || n < 0 {
			return invalid(path, "Anthropic output requires a nonnegative integer")
		}
		return nil
	}
	usage := func(v p.Value, base string, input bool) error {
		u, e := obj(v, base)
		if e != nil {
			return e
		}
		keys := []string{"output_tokens"}
		if input {
			keys = append(keys, "input_tokens")
		}
		for _, k := range keys {
			if e := number(u[k], base+"/"+k); e != nil {
				return e
			}
		}
		for _, k := range []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			if !u[k].IsZero() {
				if e := number(u[k], base+"/"+k); e != nil {
					return e
				}
			}
		}
		return nil
	}
	message := func(v p.Value, base string) error {
		m, e := obj(v, base)
		if e != nil {
			return e
		}
		for _, k := range []string{"id", "model", "type", "role"} {
			if e := text(m[k], base+"/"+k); e != nil {
				return e
			}
		}
		if m["type"] != p.StringValue("message") || m["role"] != p.StringValue("assistant") {
			return invalid(base, "expected an assistant message")
		}
		var content []p.Value
		if m["content"].IsNull() || m["content"].Decode(&content) != nil {
			return invalid(base+"/content", "content must be an array")
		}
		for i, value := range content {
			path := fmt.Sprintf("%s/content/%d", base, i)
			block, err := obj(value, path)
			if err != nil {
				return err
			}
			if block["type"] == p.StringValue("thinking") {
				for _, key := range []string{"thinking", "signature"} {
					var value string
					if block[key].IsNull() || block[key].Decode(&value) != nil {
						return invalid(path+"/"+key, "completed thinking block requires a string")
					}
				}
			}
		}
		return usage(m["usage"], base+"/usage", true)
	}
	fields, err := obj(value, "")
	if err != nil {
		return err
	}
	if fields["type"] == p.StringValue("error") {
		_, err := obj(fields["error"], "/error")
		return err
	}
	if direction == p.EncodeResponse {
		return message(value, "")
	}
	var kind string
	if err := text(fields["type"], "/type"); err != nil {
		return err
	}
	_ = fields["type"].Decode(&kind)
	switch kind {
	case "message_start":
		return message(fields["message"], "/message")
	case "message_delta":
		d, e := obj(fields["delta"], "/delta")
		if e != nil {
			return e
		}
		if e := text(d["stop_reason"], "/delta/stop_reason"); e != nil {
			return e
		}
		return usage(fields["usage"], "/usage", false)
	case "content_block_start", "content_block_delta", "content_block_stop":
		if e := number(fields["index"], "/index"); e != nil {
			return e
		}
		field := "content_block"
		if kind == "content_block_delta" {
			field = "delta"
		}
		if kind == "content_block_stop" {
			return nil
		}
		block, e := obj(fields[field], "/"+field)
		if e != nil {
			return e
		}
		return text(block["type"], "/"+field+"/type")
	case "message_stop", "ping":
		return nil
	default:
		return invalid("/type", fmt.Sprintf("unsupported Anthropic output event %q", kind))
	}
}
