package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
)

// Keep one stable structured item throughout its stream. A completed JSON
// decoder can subsequently unwrap an exactly equivalent single text part.
func (adapter module) decodeReasoningItem(value p.Value, path string, direction p.Direction, options p.EvaluationContext) (p.Node, error) {
	fields, err := value.ReadObject()
	if err != nil {
		return p.Node{}, err
	}
	node := p.Node{Kind: p.ReasoningNode, ReasoningForm: p.StructuredReasoning, ID: fields["id"], Status: fields["status"], Native: adapter.native(value, path, direction, options)}
	if encrypted := fields["encrypted_content"]; !encrypted.IsZero() {
		node.Resources = append(node.Resources, p.Resource{Kind: "encrypted_content", ID: encrypted, Scope: options.Scope})
	}
	node.Attributes = adapter.extensions(fields, []string{"type", "id", "status", "encrypted_content", "summary", "content"})
	for _, entry := range []struct {
		field, kind string
		parts       *[]p.Node
	}{
		{"summary", "summary_text", &node.Children}, {"content", "reasoning_text", &node.ReasoningContent},
	} {
		if fields[entry.field].IsZero() {
			continue
		}
		values, err := readArray(fields[entry.field])
		if err != nil {
			return node, reasoningInputError(path+"/"+entry.field, "reasoning parts must be an array")
		}
		*entry.parts = make([]p.Node, 0, len(values))
		for index, value := range values {
			part, err := adapter.decodeReasoningPart(value, entry.kind, fmt.Sprintf("%s/%s/%d", path, entry.field, index))
			if err != nil {
				return node, err
			}
			*entry.parts = append(*entry.parts, part)
		}
	}
	return node, nil
}

func reasoningInputError(path, reason string) error {
	return p.IssuesError([]p.ConversionIssue{{Code: p.InvalidInput, Severity: p.SeverityError, Path: path, Reason: reason}})
}

func (adapter module) decodeReasoningPart(value p.Value, kind, path string) (p.Node, error) {
	part, err := value.ReadObject()
	if err != nil {
		return p.Node{}, reasoningInputError(path, "reasoning part must be an object")
	}
	if part["type"] != p.StringValue(kind) {
		return p.Node{}, reasoningInputError(path+"/type", "expected "+kind)
	}
	if _, err := stringValue(part["text"]); err != nil {
		return p.Node{}, reasoningInputError(path+"/text", "reasoning part requires string text")
	}
	return p.Node{Kind: p.TextNode, Payload: part["text"], Attributes: adapter.extensions(part, []string{"type", "text"})}, nil
}

func (adapter module) encodeReasoningParts(parts []p.Node, kind string) (p.Value, error) {
	values := make([]p.Value, 0, len(parts))
	for _, part := range parts {
		plain := part
		plain.Attributes = nil
		if !p.PlainReasoningTextPart(plain) {
			return p.Value{}, unsupported("/reasoningContent", "reasoning text parts cannot silently discard cache, resources or other node fields")
		}
		fields := p.Object{"type": p.StringValue(kind), "text": part.Payload}
		if err := adapter.preserveExtensions(fields, part.Attributes); err != nil {
			return p.Value{}, err
		}
		values = append(values, object(fields))
	}
	return array(values), nil
}
