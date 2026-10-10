package protocol

// comparableConversation removes only the protocol-required role envelope of
// a tool or thinking item. IDs, annotations, cache boundaries and explicit empty messages
// keep their envelopes because they carry additional semantics.
func comparableConversation(nodes []Node) []Node {
	var result []Node
	for _, node := range nodes {
		if node.Kind != MessageNode || !node.ID.IsZero() || !node.Status.IsZero() || len(node.Attributes) > 0 || len(node.Metadata) > 0 || len(node.Cache) > 0 || len(node.Resources) > 0 || !node.Payload.IsZero() || node.Input != nil || !node.Name.IsZero() || !node.CallID.IsZero() || len(node.Children) == 0 {
			result = append(result, node)
			continue
		}
		var children []Node
		flush := func() {
			if len(children) > 0 {
				copy := node
				copy.Children = children
				result = append(result, copy)
				children = nil
			}
		}
		for _, child := range node.Children {
			isCall := child.Kind == ToolCallNode && node.Role == StringValue("assistant")
			isResult := child.Kind == ToolResultNode && (node.Role == StringValue("user") || node.Role == StringValue("tool"))
			isThinking := child.Kind == ReasoningNode && node.Role == StringValue("assistant")
			if isCall || isResult || isThinking {
				flush()
				result = append(result, child)
			} else {
				children = append(children, child)
			}
		}
		flush()
	}
	return result
}

// Wire counters cannot carry the gateway's observed/inferred provenance.
// Compare present counts while retaining that provenance in runtime accounting.
// The uncached subtotal is redundant only when its arithmetic is exact.
func comparableWireUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	copy := &Usage{}
	normalize := func(value *Counter) *Counter {
		if value == nil {
			return nil
		}
		return &Counter{Count: value.Count, Origin: ObservedCount}
	}
	copy.Input, copy.Output, copy.Total = normalize(usage.Input), normalize(usage.Output), normalize(usage.Total)
	copy.CacheRead, copy.CacheCreation = normalize(usage.CacheRead), normalize(usage.CacheCreation)
	for name, value := range usage.Details {
		if name == "uncached_input_tokens" && usage.Input != nil {
			expected := usage.Input.Count
			for _, cached := range []*Counter{usage.CacheRead, usage.CacheCreation} {
				if cached != nil {
					expected -= cached.Count
				}
			}
			if expected == value.Count {
				continue
			}
		}
		if copy.Details == nil {
			copy.Details = map[string]Counter{}
		}
		copy.Details[name] = Counter{Count: value.Count, Origin: ObservedCount}
	}
	return copy
}

func equivalentRequest(request *Request) *Request {
	copy := *request
	copy.Content = comparableConversation(request.Content)
	// Client delivery preferences and upstream accounting controls are hop-local.
	copy.ClientOutput = nil
	return &copy
}

func equivalentResponse(response *Response, family string) *Response {
	copy := *response
	copy.Content = comparableConversation(response.Content)
	copy.Usage = comparableWireUsage(response.Usage)
	if response.Attributes["anthropic_stop_sequence"].IsNull() {
		copy.Attributes = Object{}
		for key, value := range response.Attributes {
			if key != "anthropic_stop_sequence" {
				copy.Attributes[key] = value
			}
		}
	}
	return &copy
}
