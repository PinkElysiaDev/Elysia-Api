package protocol

import "fmt"

type visibleReasoningVerificationKey struct{}

// CheckCombinationRequest applies value-dependent proof boundaries in addition
// to the full model capability and provenance checks. It does not project data.
func CheckCombinationRequest(request *Request, report CombinationReport, target Identity) []ConversionIssue {
	if !report.VisibleReasoningOnly || request == nil {
		return nil
	}
	path := nonVisibleReasoningPath(request.Content, "/content")
	if path == "" {
		return nil
	}
	return []ConversionIssue{{Code: UnsupportedCapability, Severity: SeverityError, Protocol: target, Direction: EncodeRequest,
		Stage: "binding", Path: path, Capability: ReasoningCapability,
		Reason: "verified profile supports visible thinking only; summary or multipart reasoning requires independent conversion evidence"}}
}

func nonVisibleReasoningPath(nodes []Node, base string) string {
	for i, node := range nodes {
		at := fmt.Sprintf("%s/%d", base, i)
		n := CanonicalReasoning(node)
		if n.Kind == ReasoningNode && (n.ReasoningForm != "" || len(n.Children) != 0 || n.ReasoningContent != nil) {
			return at + "/reasoningForm"
		}
		if path := nonVisibleReasoningPath(n.Children, at+"/children"); path != "" {
			return path
		}
	}
	return ""
}

func hasOnlyVisibleReasoning(value any) bool {
	switch v := value.(type) {
	case *Request:
		return nonVisibleReasoningPath(v.Content, "/content") == ""
	case *Response:
		return nonVisibleReasoningPath(v.Content, "/content") == ""
	case []Event:
		for _, event := range v {
			if event.Request != nil && !hasOnlyVisibleReasoning(event.Request) {
				return false
			}
			if event.Response != nil && !hasOnlyVisibleReasoning(event.Response) {
				return false
			}
			if event.Item != nil {
				n := *event.Item
				// An empty start has not yet chosen summary or visible content.
				if event.Type == ItemStarted && n.Kind == ReasoningNode && len(n.Children) == 0 && len(n.ReasoningContent) == 0 && n.Payload.IsZero() {
					continue
				}
				if nonVisibleReasoningPath([]Node{n}, "/item") != "" {
					return false
				}
			}
		}
	}
	return true
}
