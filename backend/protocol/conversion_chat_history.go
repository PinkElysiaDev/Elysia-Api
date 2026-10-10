package protocol

import "fmt"

// chatHistory projects one contiguous assistant turn before encoding. Tool
// results and other roles end the turn; unfinished history is never discarded.
func (c *CompiledConversion) chatHistory(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var result, pending []Node
	start, bare := 0, false
	changed := false
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		defer func() { pending, bare = nil, false }()
		if !bare {
			// Native Chat message boundaries remain intact. Other codecs can
			// still place text between calls inside one assistant message.
			for i, n := range pending {
				children, reordered, err := c.chatHistoryChildren(n.Children, fmt.Sprintf("/content/%d/children", start+i), route, rule, sink)
				if err != nil {
					return err
				}
				if reordered {
					n.Children, n.Native = children, nil
					changed = true
				}
				result = append(result, n)
			}
			return nil
		}
		at := fmt.Sprintf("/content/%d", start)
		n := Node{Kind: MessageNode, Role: StringValue("assistant")}
		messages := 0
		for i, item := range pending {
			path := fmt.Sprintf("/content/%d", start+i)
			if item.Kind == MessageNode {
				messages++
				if len(item.Cache) > 0 || len(item.Resources) > 0 || len(item.Attributes) > 0 || len(item.Metadata) > 0 || !item.Payload.IsZero() || !item.Name.IsZero() || !item.CallID.IsZero() || item.Input != nil {
					return streamIssue(UnsupportedCapability, path, "assistant item grouping cannot move message metadata across thinking, text or tools")
				}
			}
			if !item.ID.IsZero() || !item.Status.IsZero() {
				if err := c.issue(rule, ConversionRequest, route, path, "Chat cannot express separate history item identity/status; tool call IDs are retained", sink, true); err != nil {
					return err
				}
				item.ID, item.Status, item.Native = Value{}, Value{}, nil
			}
			if item.Kind == MessageNode {
				n.Children = append(n.Children, item.Children...)
			} else {
				n.Children = append(n.Children, item)
			}
		}
		if messages > 1 {
			if err := c.issue(rule, ConversionRequest, route, at, "Chat combines message boundaries within this assistant turn to keep parallel tool calls adjacent to their results", sink, true); err != nil {
				return err
			}
		}
		children, _, err := c.chatHistoryChildren(n.Children, at+"/children", route, rule, sink)
		if err != nil {
			return err
		}
		n.Children = children
		hasReply := false
		for _, child := range n.Children {
			hasReply = hasReply || child.Kind == TextNode || child.Kind == ToolCallNode || child.Kind == RefusalNode
		}
		if !hasReply {
			return streamIssue(UnsupportedCapability, at, "Chat thinking history requires associated assistant content or tool calls before the next turn")
		}
		result = append(result, n)
		changed = true
		c.normalized(rule, route, at, "thinking, text and tool output items attached to their assistant history turn", sink)
		return nil
	}
	for i, node := range request.Content {
		if node.Kind == ReasoningNode || node.Kind == ToolCallNode || (node.Kind == MessageNode && node.Role == StringValue("assistant")) {
			if len(pending) == 0 {
				start = i
			}
			bare = bare || node.Kind != MessageNode
			pending = append(pending, node)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		result = append(result, node)
	}
	if err := flush(); err != nil {
		return err
	}
	if changed {
		request.Content, request.Native = result, nil
	}
	return nil
}

// Chat exposes separate reasoning, content, calls and refusal fields. Project
// that order explicitly so round-trip verification sees the same target data.
func (c *CompiledConversion) chatHistoryChildren(nodes []Node, at string, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) ([]Node, bool, error) {
	var groups [4][]Node
	previous, thoughts := -1, 0
	reordered := false
	for i, n := range nodes {
		rank := 1
		switch n.Kind {
		case ReasoningNode:
			rank, thoughts = 0, thoughts+1
			if thoughts > 1 || i != 0 {
				return nil, false, streamIssue(UnsupportedCapability, at, "Chat cannot preserve multiple or interleaved thinking items in one history turn")
			}
		case ToolCallNode:
			rank = 2
		case RefusalNode:
			rank = 3
		case ToolResultNode, MessageNode:
			return nil, false, streamIssue(UnsupportedCapability, at, "assistant history cannot absorb tool results or nested messages")
		}
		reordered = reordered || rank < previous
		previous = rank
		groups[rank] = append(groups[rank], n)
	}
	if !reordered {
		return nodes, false, nil
	}
	if err := c.issue(rule, ConversionRequest, route, at, "Chat separates content from tool calls and cannot retain their interleaving; text order and tool call IDs are retained", sink, true); err != nil {
		return nil, false, err
	}
	var out []Node
	for _, group := range groups {
		out = append(out, group...)
	}
	return out, true, nil
}
