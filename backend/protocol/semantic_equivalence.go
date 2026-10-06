package protocol

// comparableConversation removes only the protocol-required role envelope of
// a tool item. IDs, annotations, cache boundaries and explicit empty messages
// keep their envelopes because they carry additional semantics.
func comparableConversation(nodes []Node) []Node {
	var result []Node
	for _, node := range nodes {
		if node.Kind != MessageNode || !node.ID.IsZero() || !node.Status.IsZero() || len(node.Attributes) > 0 || len(node.Cache) > 0 || len(node.Resources) > 0 || !node.Payload.IsZero() || node.Input != nil || !node.Name.IsZero() || !node.CallID.IsZero() || len(node.Children) == 0 {
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
			if isCall || isResult {
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

// anthropicCacheBucketFamily owns the provider TTL bucket schema. Every other
// family keeps only the creation total, so a cross-protocol roundtrip legally
// projects these details away and the comparison must not read that as loss.
const anthropicCacheBucketFamily = "claude"

// geminiCacheReadOnlyFamily reports cache reads but has no wire field for the
// provider's cache creation total.
const geminiCacheReadOnlyFamily = "gemini"

// cacheOmission returns the approved usage projections for a target family:
// nil when the family preserves every counter the provider reports.
func cacheOmission(family string) func(string) bool {
	if family == anthropicCacheBucketFamily {
		return nil
	}
	return func(name string) bool {
		if name == "ephemeral_5m_input_tokens" || name == "ephemeral_1h_input_tokens" {
			return true
		}
		// Gemini has no cache creation counter; the total is projected away.
		return name == "/usage/cacheCreation" && family == geminiCacheReadOnlyFamily
	}
}

// Wire counters cannot carry the gateway's observed/inferred provenance.
// Compare present counts while retaining that provenance in runtime accounting.
// The uncached subtotal is redundant only when its arithmetic is exact.
func comparableWireUsage(usage *Usage, omitted func(string) bool) *Usage {
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
	if omitted != nil && omitted("/usage/cacheCreation") {
		copy.CacheCreation = nil
	}
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
		if omitted != nil && omitted(name) {
			continue
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
	return &copy
}

func equivalentResponse(response *Response, family string) *Response {
	copy := *response
	copy.Content = comparableConversation(response.Content)
	copy.Usage = comparableWireUsage(response.Usage, cacheOmission(family))
	return &copy
}
