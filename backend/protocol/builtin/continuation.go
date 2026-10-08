package builtin

import (
	"crypto/sha256"
	"fmt"
	"strings"

	p "github.com/elysia-api/backend/protocol"
)

// ExtractContinuationCarriers recognizes only our reserved carriers at protocol
// history positions. It never traverses user text, tool arguments or results.
func ExtractContinuationCarriers(body []byte, family string, limit int) ([]byte, []string, error) {
	root, err := p.ParseValue(body)
	if err != nil {
		return nil, nil, err
	}
	fields, err := root.ReadObject()
	if err != nil {
		return nil, nil, err
	}
	var tokens []string
	bytes := 0
	add := func(v p.Value) bool {
		var token string
		if v.Decode(&token) != nil || !strings.HasPrefix(token, p.ContinuationPrefix) {
			return false
		}
		bytes += len(token)
		tokens = append(tokens, token)
		return true
	}
	stripBlocks := func(raw p.Value) (p.Value, error) {
		var blocks []p.Value
		if raw.Decode(&blocks) != nil {
			return raw, nil
		}
		out := make([]p.Value, 0, len(blocks))
		for _, block := range blocks {
			b, e := block.ReadObject()
			if e != nil {
				out = append(out, block)
				continue
			}
			typ, _ := stringValue(b["type"])
			if typ == "thinking" && add(b["signature"]) {
				if b["thinking"].IsZero() || b["thinking"] == p.StringValue("") {
					continue
				}
				delete(b, "signature")
				out = append(out, object(b))
				continue
			}
			if typ == "reasoning" && add(b["encrypted_content"]) {
				continue
			}
			out = append(out, block)
		}
		return p.EncodeValue(out)
	}
	history := "messages"
	if family == "openai_responses" {
		history = "input"
	}
	var messages []p.Value
	if fields[history].Decode(&messages) != nil {
		return body, nil, nil
	}
	var out []p.Value
	for _, message := range messages {
		m, e := message.ReadObject()
		if e != nil {
			out = append(out, message)
			continue
		}
		if family == "openai_responses" && m["type"] == p.StringValue("reasoning") && add(m["encrypted_content"]) {
			continue
		}
		if m["role"] == p.StringValue("assistant") {
			if family == "openai_chat" {
				var carriers []p.Value
				if v := m["elysia_continuation"]; !v.IsZero() {
					if err := v.Decode(&carriers); err != nil {
						return nil, nil, fmt.Errorf("invalid elysia_continuation carrier")
					}
					for _, v := range carriers {
						if !add(v) {
							return nil, nil, fmt.Errorf("invalid elysia continuation prefix")
						}
					}
					delete(m, "elysia_continuation")
				}
			}
			if family == "claude" {
				m["content"], err = stripBlocks(m["content"])
				if err != nil {
					return nil, nil, err
				}
			}
		}
		value, err := p.EncodeValue(m)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, value)
	}
	if bytes > limit || len(tokens) > p.DefaultLimits().StateItems {
		return nil, nil, fmt.Errorf("continuation carriers exceed limit")
	}
	if len(tokens) == 0 {
		return body, nil, nil
	}
	fields[history], err = p.EncodeValue(out)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := p.EncodeValue(fields)
	return encoded.Bytes(), tokens, err
}

func carrierBlocks(tokens []string, family string) []p.Value {
	out := []p.Value{}
	for _, token := range tokens {
		var fields p.Object
		switch family {
		case "claude":
			fields = p.Object{"type": p.StringValue("thinking"), "thinking": p.StringValue(""), "signature": p.StringValue(token)}
		case "openai_responses":
			fields = p.Object{"type": p.StringValue("reasoning"), "id": p.StringValue(fmt.Sprintf("elysia_continuation_%x", sha256.Sum256([]byte(token)))), "summary": array(nil), "encrypted_content": p.StringValue(token)}
		default:
			continue
		}
		out = append(out, object(fields))
	}
	return out
}

func AttachContinuationCarriers(body []byte, family string, tokens []string) ([]byte, error) {
	if len(tokens) == 0 {
		return body, nil
	}
	root, err := p.ParseValue(body)
	if err != nil {
		return nil, err
	}
	fields, err := root.ReadObject()
	if err != nil {
		return nil, err
	}
	switch family {
	case "claude", "openai_responses":
		key := "content"
		if family == "openai_responses" {
			key = "output"
		}
		var content []p.Value
		if !fields[key].IsZero() {
			if err := fields[key].Decode(&content); err != nil {
				return nil, err
			}
		}
		content = append(content, carrierBlocks(tokens, family)...)
		fields[key] = array(content)
	case "openai_chat":
		var choices []p.Value
		if err := fields["choices"].Decode(&choices); err != nil {
			return nil, err
		}
		if len(choices) != 1 {
			return nil, fmt.Errorf("continuation requires exactly one choice")
		}
		choice, err := choices[0].ReadObject()
		if err != nil {
			return nil, err
		}
		message, err := choice["message"].ReadObject()
		if err != nil {
			return nil, err
		}
		message["elysia_continuation"], err = p.EncodeValue(tokens)
		if err != nil {
			return nil, err
		}
		choice["message"] = object(message)
		choices[0] = object(choice)
		fields["choices"] = array(choices)
	default:
		return body, nil
	}
	value, err := p.EncodeValue(fields)
	return value.Bytes(), err
}

// ContinuationStream inserts carriers at a protocol terminal boundary, after
// content blocks have closed. Original content order is never changed.
type ContinuationStream struct {
	Family   string
	Tokens   []string
	maxIndex int
	sequence int
	emitted  bool
}

func (stream *ContinuationStream) Frames(value p.Value) ([]p.Value, error) {
	nullValue, _ := p.EncodeValue(nil)
	fields, err := value.ReadObject()
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"index", "output_index"} {
		var index int
		if fields[key].Decode(&index) == nil && index >= stream.maxIndex {
			stream.maxIndex = index + 1
		}
	}
	var typ string
	_ = fields["type"].Decode(&typ)
	result := []p.Value{}
	if !stream.emitted && len(stream.Tokens) > 0 {
		switch {
		case stream.Family == "claude" && (typ == "message_delta" || typ == "message_stop"):
			for _, block := range carrierBlocks(stream.Tokens, stream.Family) {
				idx, _ := p.EncodeValue(stream.maxIndex)
				stream.maxIndex++
				b, _ := block.ReadObject()
				result = append(result,
					object(p.Object{"type": p.StringValue("content_block_start"), "index": idx, "content_block": object(p.Object{"type": p.StringValue("thinking"), "thinking": p.StringValue("")})}),
					object(p.Object{"type": p.StringValue("content_block_delta"), "index": idx, "delta": object(p.Object{"type": p.StringValue("signature_delta"), "signature": b["signature"]})}),
					object(p.Object{"type": p.StringValue("content_block_stop"), "index": idx}))
			}
			stream.emitted = true
		case stream.Family == "openai_chat":
			var choices []p.Value
			_ = fields["choices"].Decode(&choices)
			isTerminal := len(choices) == 0
			if len(choices) > 0 {
				first, _ := choices[0].ReadObject()
				isTerminal = !first["finish_reason"].IsZero() && !first["finish_reason"].IsNull()
			}
			if isTerminal {
				tokens, _ := p.EncodeValue(stream.Tokens)
				idx, _ := p.EncodeValue(0)
				result = append(result, object(p.Object{"id": fields["id"], "model": fields["model"], "object": p.StringValue("chat.completion.chunk"), "choices": array([]p.Value{object(p.Object{"index": idx, "delta": object(p.Object{"elysia_continuation": tokens}), "finish_reason": nullValue})})}))
				stream.emitted = true
			}
		case stream.Family == "openai_responses" && (typ == "response.completed" || typ == "response.incomplete"):
			response := fields["response"]
			body, e := AttachContinuationCarriers(response.Bytes(), stream.Family, stream.Tokens)
			if e != nil {
				return nil, e
			}
			fields["response"], err = p.ParseValue(body)
			if err != nil {
				return nil, err
			}
			for _, block := range carrierBlocks(stream.Tokens, stream.Family) {
				idx, _ := p.EncodeValue(stream.maxIndex)
				stream.maxIndex++
				for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
					result = append(result, object(p.Object{"type": p.StringValue(kind), "output_index": idx, "item": block}))
				}
			}
			value = object(fields)
			stream.emitted = true
		}
	}
	result = append(result, value)
	if stream.Family == "openai_responses" {
		for i, v := range result {
			f, e := v.ReadObject()
			if e != nil {
				return nil, e
			}
			f["sequence_number"], _ = p.EncodeValue(stream.sequence)
			stream.sequence++
			result[i] = object(f)
		}
	}
	return result, nil
}
