package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func validateToolResultErrors(nodes []Node, at string) error {
	for i, n := range nodes {
		path := fmt.Sprintf("%s/%d", at, i)
		if n.Kind == ToolResultNode {
			fields, _ := n.Status.ReadObject()
			if value := fields["isError"]; !value.IsZero() {
				var flag bool
				if value.IsNull() || value.Decode(&flag) != nil {
					return streamIssue(InvalidInput, path+"/status/isError", "tool error flag must be a boolean")
				}
			}
		}
		if err := validateToolResultErrors(n.Children, path+"/children"); err != nil {
			return err
		}
		if err := validateToolResultErrors(n.ReasoningContent, path+"/reasoningContent"); err != nil {
			return err
		}
	}
	return nil
}

func toolResultError(status Value, path string) (bool, error) {
	if status.IsZero() {
		return false, nil
	}
	fields, err := status.ReadObject()
	if err != nil || len(fields) != 1 || fields["isError"].IsZero() {
		return false, streamIssue(UnsupportedCapability, path, "tool result status requires one boolean error flag")
	}
	var failed bool
	if fields["isError"].IsNull() || fields["isError"].Decode(&failed) != nil {
		return false, streamIssue(InvalidInput, path+"/isError", "tool error flag must be a boolean")
	}
	return failed, nil
}

func (c *CompiledConversion) toolResult(n *Node, route ConversionContext, rule ConversionRule, at string, sink *DiagnosticSink) error {
	failed, err := toolResultError(n.Status, at+"/status")
	if err != nil {
		return err
	}
	if n.Children == nil && (rule.Action == "tool_result_object" || rule.Value.IsString("openai-chat") || rule.Value.IsString("responses")) && !n.Payload.IsZero() && !n.Payload.IsNull() {
		if blocks, err := n.Payload.readArray(); err == nil && len(blocks) == 0 {
			n.Payload = StringValue("")
			c.normalized(rule, route, at+"/payload", "empty tool result blocks normalized to empty text", sink)
		}
	}
	if rule.Action == "tool_result_text" {
		if n.Payload.IsObject() {
			if err := c.issue(rule, ConversionRequest, route, at+"/payload", "object tool result serialized as JSON text; payload type changes", sink, true); err != nil {
				return err
			}
			n.Payload = StringValue(n.Payload.raw)
		}
		var codec string
		_ = rule.Value.Decode(&codec)
		if n.Status.IsZero() || (codec != "openai-chat" && codec != "responses") {
			return nil
		}
		if failed {
			if err := c.issue(rule, ConversionRequest, route, at+"/status", "tool error flag represented by a text marker; target has no separate error flag", sink, true); err != nil {
				return err
			}
			const marker = "[Tool error] "
			if n.Children != nil {
				n.Children = append([]Node{{Kind: TextNode, Payload: StringValue(marker)}}, n.Children...)
			} else {
				var text string
				if (!n.Payload.IsZero() && n.Payload.Decode(&text) != nil) || n.Payload.IsNull() {
					return streamIssue(InvalidInput, at+"/payload", "tool result requires text or content blocks")
				}
				n.Payload = StringValue(marker + text)
			}
		} else {
			c.normalized(rule, route, at+"/status", "false tool error flag normalized to an ordinary result", sink)
		}
		n.Status = Value{}
		return nil
	}

	if n.Children != nil {
		var text strings.Builder
		for i, block := range n.Children {
			path := fmt.Sprintf("%s/children/%d", at, i)
			if block.Kind != TextNode || block.ReasoningForm != "" || block.ReasoningContent != nil || !block.Role.IsZero() || !block.ID.IsZero() || !block.CallID.IsZero() || !block.Name.IsZero() || !block.Status.IsZero() || block.Input != nil || len(block.Children) > 0 || len(block.Cache) > 0 || len(block.Resources) > 0 || len(block.Attributes) > 0 || len(block.Metadata) > 0 {
				return candidateFailure(streamIssue(UnsupportedCapability, path, "object tool results require plain text blocks; media and scoped state need explicit mapping"))
			}
			var value string
			if block.Payload.IsNull() || block.Payload.Decode(&value) != nil {
				return streamIssue(InvalidInput, path+"/payload", "text payload must be a string")
			}
			text.WriteString(value)
		}
		if err := c.issue(rule, ConversionRequest, route, at+"/children", "tool result text blocks combined into object content; block boundaries change", sink, true); err != nil {
			return err
		}
		n.Payload, n.Children = StringValue(text.String()), nil
	}
	if !n.Payload.IsObject() {
		text, ok := canonicalToolText(n.Payload)
		if !ok {
			return streamIssue(InvalidInput, at+"/payload", "tool_result_object requires text or an object")
		}
		key := "result"
		if !rule.Value.IsZero() && (rule.Value.Decode(&key) != nil || key == "") {
			return fmt.Errorf("tool_result_object requires a nonempty field name")
		}
		if err := c.issue(rule, ConversionRequest, route, at+"/payload", "text tool result wrapped in the configured object field", sink, true); err != nil {
			return err
		}
		n.Payload, _ = EncodeValue(Object{key: text})
	}
	if !n.Status.IsZero() {
		if failed {
			n.Payload, _ = EncodeValue(Object{"error": n.Payload})
		} else if fields, _ := n.Payload.ReadObject(); !fields["error"].IsZero() || !fields["output"].IsZero() {
			// Reserved response keys in caller data still belong to successful output.
			n.Payload, _ = EncodeValue(Object{"output": n.Payload})
		}
		c.normalized(rule, route, at+"/status", "tool result error state mapped to object response semantics", sink)
		n.Status = Value{}
	}
	return nil
}

// Plain JSON strings already have the spelling produced by Decode + StringValue
// (HTML escaping is applied by the enclosing object encoder). Escapes and invalid
// UTF-8 retain the old local normalization; unrelated media is never decoded.
func canonicalToolText(value Value) (Value, bool) {
	if value.IsZero() {
		return StringValue(""), true
	}
	raw := strings.TrimSpace(value.raw)
	if len(raw) > 0 && raw[0] == '"' && !strings.Contains(raw, `\`) && utf8.ValidString(raw) {
		return Value{raw: raw}, true
	}
	var text string
	if value.IsNull() || value.Decode(&text) != nil {
		return Value{}, false
	}
	return StringValue(text), true
}
