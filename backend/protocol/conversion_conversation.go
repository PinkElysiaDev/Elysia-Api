package protocol

import (
	"fmt"
	"strings"
)

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
		if node.Source != nil && node.Source.Path != "" {
			at = node.Source.Path
		}
		if field := systemInstructionMetadataField(node); field != "" {
			return c.rejectSystemHoist(rule, route, at+field)
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
		// The projected envelope no longer represents the original inline
		// message. Keep its source evidence, but do not replay that container.
		combined.Native = nil
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

func systemInstructionMetadataField(node Node) string {
	switch {
	case !node.ID.IsZero():
		return "/id"
	case !node.Status.IsZero():
		return "/status"
	case len(node.Resources) > 0:
		return "/resources"
	case len(node.Cache) > 0:
		return "/cache_control"
	}
	keys := sortedKeys(node.Attributes)
	if len(keys) == 0 {
		return ""
	}
	key := keys[0]
	if fields, err := node.Attributes[key].ReadObject(); err == nil && len(fields) > 0 && strings.HasPrefix(key, "wire:") {
		return "/" + escapePointer(sortedKeys(fields)[0])
	}
	return "/attributes/" + escapePointer(key)
}

func (c *CompiledConversion) rejectSystemHoist(rule ConversionRule, route ConversionContext, at string) error {
	return candidateFailure(IssuesError([]ConversionIssue{{
		Code: UnsupportedCapability, Severity: SeverityError, Fidelity: "rejected",
		Protocol: route.Target, Direction: EncodeRequest, Stage: "conversion.request",
		Path: at, Reason: "system instruction metadata requires an explicit block-level mapping before hoisting",
		RuleID: rule.ID, PolicyHash: c.Hash, PolicyRevision: c.RuleRevisions[rule.ID], Evidence: c.Origins[rule.ID],
		Suggestion: "Provide an explicit request mapping that preserves this field's scope before hoisting.",
	}}))
}
