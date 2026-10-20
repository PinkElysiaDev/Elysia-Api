package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) ValidateWireOutput(direction p.Direction, value p.Value) error {
	if direction != p.EncodeRequest && direction != p.EncodeResponse && direction != p.EncodeEvent {
		return nil
	}
	fields, err := readWireObject(value, "")
	if err != nil {
		return err
	}
	return adapter.ValidateWireObjectOutput(direction, value, fields)
}

// ValidateWireObjectOutput never mutates or retains the caller's field map.
func (adapter module) ValidateWireObjectOutput(direction p.Direction, value p.Value, fields p.Object) error {
	if err := adapter.validateWireObjectContract(direction, value, fields); err != nil {
		return err
	}
	if adapter.name != Anthropic || (direction != p.EncodeResponse && direction != p.EncodeEvent) {
		return nil
	}
	usage := func(v p.Value, base string, hasInput bool) error {
		u, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		keys := []string{"output_tokens"}
		if hasInput {
			keys = append(keys, "input_tokens")
		}
		for _, k := range keys {
			if e := requireWireInteger(u[k], base+"/"+k); e != nil {
				return e
			}
		}
		for _, k := range []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			if !(hasInput && k == "input_tokens") && !u[k].IsZero() {
				if e := requireWireInteger(u[k], base+"/"+k); e != nil {
					return e
				}
			}
		}
		return nil
	}
	message := func(v p.Value, base string) error {
		m, e := readWireObject(v, base)
		if e != nil {
			return e
		}
		for _, k := range []string{"id", "model", "type", "role"} {
			if e := requireWireString(m[k], base+"/"+k); e != nil {
				return e
			}
		}
		if !m["type"].IsString("message") || !m["role"].IsString("assistant") {
			return wireContractError(base, "expected an assistant message")
		}
		var content []p.Value
		if m["content"].IsNull() || m["content"].Decode(&content) != nil {
			return wireContractError(base+"/content", "content must be an array")
		}
		for i, value := range content {
			path := fmt.Sprintf("%s/content/%d", base, i)
			block, err := readWireObject(value, path)
			if err != nil {
				return err
			}
			if block["type"].IsString("thinking") {
				for _, key := range []string{"thinking", "signature"} {
					var value string
					if block[key].IsNull() || block[key].Decode(&value) != nil {
						return wireContractError(path+"/"+key, "completed thinking block requires a string")
					}
				}
			}
		}
		return usage(m["usage"], base+"/usage", true)
	}
	if fields["type"].IsString("error") {
		_, err := readWireObject(fields["error"], "/error")
		return err
	}
	if direction == p.EncodeResponse {
		return message(value, "")
	}
	var kind string
	if err := requireWireString(fields["type"], "/type"); err != nil {
		return err
	}
	_ = fields["type"].Decode(&kind)
	switch kind {
	case "message_start":
		return message(fields["message"], "/message")
	case "message_delta":
		d, e := readWireObject(fields["delta"], "/delta")
		if e != nil {
			return e
		}
		if e := requireWireString(d["stop_reason"], "/delta/stop_reason"); e != nil {
			return e
		}
		return usage(fields["usage"], "/usage", false)
	case "content_block_start", "content_block_delta", "content_block_stop":
		if e := requireWireInteger(fields["index"], "/index"); e != nil {
			return e
		}
		field := "content_block"
		if kind == "content_block_delta" {
			field = "delta"
		}
		if kind == "content_block_stop" {
			return nil
		}
		block, e := readWireObject(fields[field], "/"+field)
		if e != nil {
			return e
		}
		return requireWireString(block["type"], "/"+field+"/type")
	case "message_stop", "ping":
		return nil
	default:
		return wireContractError("/type", fmt.Sprintf("unsupported Anthropic output event %q", kind))
	}
}
