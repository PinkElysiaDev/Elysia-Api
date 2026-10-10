package protocol

import "fmt"

// chatHistory attaches independent assistant output items to their turn before
// encoding. It never crosses a user/tool-result boundary or invents a reply.
func (c *CompiledConversion) chatHistory(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var result, pending []Node
	start := 0
	changed := false
	flush := func(message *Node) error {
		if len(pending) == 0 {
			return nil
		}
		at := fmt.Sprintf("/content/%d", start)
		n := Node{Kind: MessageNode, Role: StringValue("assistant")}
		if message != nil {
			n = *message
			if len(n.Cache) > 0 || len(n.Resources) > 0 || len(n.Attributes) > 0 || len(n.Metadata) > 0 {
				return streamIssue(UnsupportedCapability, at, "assistant item grouping cannot move message metadata over preceding thinking or tools")
			}
		}
		n.Children = append(append([]Node(nil), pending...), n.Children...)
		hasReply := false
		for _, child := range n.Children {
			hasReply = hasReply || child.Kind == TextNode || child.Kind == ToolCallNode || child.Kind == RefusalNode
		}
		if !hasReply {
			return streamIssue(UnsupportedCapability, at, "Chat thinking history requires associated assistant content or tool calls before the next turn")
		}
		for _, node := range append([]Node{n}, pending...) {
			if !node.ID.IsZero() || !node.Status.IsZero() {
				if err := c.issue(rule, ConversionRequest, route, at, "Chat cannot express separate history item identity/status; tool call IDs and original order are retained", sink, true); err != nil {
					return err
				}
			}
		}
		n.ID, n.Status, n.Native = Value{}, Value{}, nil
		for i := 0; i < len(pending); i++ {
			n.Children[i].ID, n.Children[i].Status = Value{}, Value{}
		}
		result = append(result, n)
		pending = nil
		changed = true
		c.normalized(rule, route, at, "ordered thinking and tool output items attached to their assistant history turn", sink)
		return nil
	}
	for i, node := range request.Content {
		if node.Kind == ReasoningNode || node.Kind == ToolCallNode {
			if node.Kind == ReasoningNode && len(pending) > 0 {
				return streamIssue(UnsupportedCapability, fmt.Sprintf("/content/%d", i), "Chat cannot preserve multiple or interleaved thinking items in one history turn")
			}
			if len(pending) == 0 {
				start = i
			}
			pending = append(pending, node)
			continue
		}
		if len(pending) == 1 && pending[0].Kind == ReasoningNode && node.Kind == MessageNode && node.Role == StringValue("assistant") {
			if err := flush(&node); err != nil {
				return err
			}
			continue
		}
		if err := flush(nil); err != nil {
			return err
		}
		result = append(result, node)
	}
	if err := flush(nil); err != nil {
		return err
	}
	if changed {
		request.Content = result
		request.Native = nil
	}
	return nil
}
