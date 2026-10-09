package protocol

// CanonicalReasoning removes only an exactly redundant text container. It
// never joins parts, relabels a summary, drops extensions or changes resources.
// Decoders, completed collectors and comparison use the same representation;
// an in-progress structured item keeps its form until the stream has finished.
func CanonicalReasoning(node Node) Node {
	if node.Kind != ReasoningNode || node.ReasoningForm != StructuredReasoning || !node.Payload.IsZero() {
		return node
	}
	if node.ReasoningContent == nil {
		node.ReasoningForm = SummaryReasoning
		return node
	}
	if len(node.Children) != 0 || len(node.ReasoningContent) > 1 {
		return node
	}
	if len(node.ReasoningContent) == 1 {
		part := node.ReasoningContent[0]
		if !PlainReasoningTextPart(part) {
			return node
		}
		node.Payload = part.Payload
	}
	node.ReasoningForm, node.ReasoningContent = "", nil
	return node
}

// PlainReasoningTextPart excludes fields that would be lost by unwrapping.
func PlainReasoningTextPart(part Node) bool {
	if part.Kind != TextNode || part.Payload.IsZero() || part.Payload.IsNull() {
		return false
	}
	var text string
	return part.Payload.Decode(&text) == nil && part.ReasoningForm == "" && part.ReasoningContent == nil &&
		part.Role.IsZero() && part.ID.IsZero() && part.CallID.IsZero() && part.Name.IsZero() && part.Status.IsZero() &&
		part.Input == nil && len(part.Children) == 0 && len(part.Cache) == 0 && len(part.Resources) == 0 &&
		len(part.Attributes) == 0 && len(part.Metadata) == 0
}
