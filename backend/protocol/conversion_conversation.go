package protocol

import "fmt"

// hoistSystem operates on the candidate copy, before both encoding and the
// round-trip check. It never grants a role or removes resource/cache evidence.
func (c *CompiledConversion) hoistSystem(request *Request, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) error {
	var system, conversation []Node
	seenConversation := false
	changed := false
	for i, node := range request.Content {
		if node.Kind != MessageNode || (node.Role != StringValue("system") && node.Role != StringValue("developer")) {
			seenConversation = true
			conversation = append(conversation, node)
			continue
		}
		at := fmt.Sprintf("/content/%d", i)
		if !node.ID.IsZero() || !node.Status.IsZero() || len(node.Resources) > 0 || len(node.Attributes) > 0 || len(node.Cache) > 0 {
			return streamIssue(UnsupportedCapability, at, "system instruction metadata requires an explicit block-level mapping before hoisting")
		}
		if seenConversation || node.Role == StringValue("developer") {
			if err := c.issue(rule, ConversionRequest, route, at, "system/developer instruction moved to top-level system; its scope or role precedence changes", sink, true); err != nil {
				return err
			}
			changed = true
		}
		node.Role = StringValue("system")
		system = append(system, node)
	}
	// One top-level system envelope preserves the original relative block order.
	if len(system) > 0 {
		combined := system[0]
		combined.Children = append([]Node(nil), combined.Children...)
		for _, node := range system[1:] {
			combined.Children = append(combined.Children, node.Children...)
		}
		request.Content = append([]Node{combined}, conversation...)
		if changed || len(system) > 1 {
			// The old message array cannot be replayed after this explicit
			// structural projection. Reconciliation would try to encode the
			// unhoisted baseline and reject it again on same-codec routes.
			// Keep semantic attributes, cache/resources and all node provenance.
			request.Native = nil
		}
	}
	return nil
}
