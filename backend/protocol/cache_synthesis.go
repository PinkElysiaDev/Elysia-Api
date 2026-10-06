package protocol

// maxCacheBreakpoints is the explicit-breakpoint limit shared by
// Anthropic-compatible targets. Synthesis never exceeds it, and a caller that
// already exceeds it is left untouched rather than edited to fit.
const maxCacheBreakpoints = 4

// SynthesizeCacheBreakpoints adds structural cache breakpoints to a decoded
// request when the target declares cache.breakpoints and the caller left room
// under the provider limit. It returns the number placed.
//
// The placement is structural, not content-derived: the provider's cache
// hierarchy is tools -> system -> messages, so marking the last entry of each
// section caches the longest stable prefix without pinning content that varies
// per turn. Caller markers are preserved verbatim; a slot that already carries
// one is skipped because one wire location expresses one policy.
//
// This is opt-in at the call site. It runs after decode and before encode, so
// every adapter observes the same request.
func SynthesizeCacheBreakpoints(request *Request, target CapabilitySet, options EvaluationContext) int {
	if request == nil || !target[CacheBreakpointsCapability] {
		return 0
	}
	existing := countCacheBreakpoints(request)
	if existing >= maxCacheBreakpoints {
		// A caller above the provider limit is the provider's to reject. Record
		// it so the excess is visible instead of silently trimmed.
		if existing > maxCacheBreakpoints {
			options.Diagnostics.Add(ConversionIssue{
				Code: UnsupportedCapability, Severity: SeverityWarning, Protocol: options.Identity(),
				Direction: EncodeRequest, Stage: "synthesis", Path: "/cache",
				Reason:     "caller supplied more cache breakpoints than the provider accepts",
				Suggestion: "Reduce the caller's own breakpoints; synthesis never edits a marker it did not place.",
			})
		}
		return 0
	}
	budget := maxCacheBreakpoints - existing
	injected := 0
	place := func(host *[]CacheIntent) {
		if budget <= 0 || len(*host) > 0 {
			return
		}
		*host = append(*host, CacheIntent{Kind: "breakpoint", Location: "block", Value: ephemeralPolicy()})
		budget--
		injected++
	}
	// Tools sit earliest in the prefix, so the last tool is the stablest anchor.
	if count := len(request.Tools); count > 0 {
		tool := &request.Tools[count-1].Cache
		if len(*tool) == 0 && budget > 0 {
			*tool = append(*tool, CacheIntent{Kind: "breakpoint", Location: "tool", Value: ephemeralPolicy()})
			budget--
			injected++
		}
	}
	if node := anchorNode(request.Content, true); node != nil {
		place(&node.Cache)
	}
	// The message tail extends the prefix past system; it is the anchor the
	// provider itself advances under automatic caching.
	if node := anchorNode(request.Content, false); node != nil {
		place(&node.Cache)
	}
	return injected
}

// anchorNode returns the node that owns the wire cache_control for a message.
// Only block-level intent reaches the wire: a message envelope drops it, and a
// system envelope rejects it outright. The anchor is therefore the last
// cacheable child, and a message with no such child yields nothing.
func anchorNode(nodes []Node, system bool) *Node {
	index := len(nodes) - 1
	if system {
		index = lastSystemIndex(nodes)
	}
	if index < 0 {
		return nil
	}
	node := &nodes[index]
	for child := len(node.Children) - 1; child >= 0; child-- {
		if isCacheableBlock(node.Children[child]) {
			return &node.Children[child]
		}
	}
	if node.Kind == MessageNode {
		// A system message with no cacheable child has nowhere to carry a
		// marker; a conversation message is escalated by the child loop above.
		return nil
	}
	return node
}

// A reasoning block carries provider state that changes every turn, so a cache
// boundary on one would never match a later prefix.
func isCacheableBlock(node Node) bool {
	return node.Kind != ReasoningNode
}

func lastSystemIndex(nodes []Node) int {
	for index := len(nodes) - 1; index >= 0; index-- {
		if nodes[index].Kind == MessageNode && roleName(nodes[index]) == "system" {
			return index
		}
	}
	return -1
}

func roleName(node Node) string {
	var role string
	if err := node.Role.Decode(&role); err != nil {
		return ""
	}
	return role
}

func countCacheBreakpoints(request *Request) int {
	count := len(request.Cache)
	for _, tool := range request.Tools {
		count += len(tool.Cache)
	}
	var visit func([]Node)
	visit = func(nodes []Node) {
		for index := range nodes {
			count += len(nodes[index].Cache)
			visit(nodes[index].Children)
		}
	}
	visit(request.Content)
	return count
}

// The provider's default breakpoint policy. Only the type is set; TTL is left
// to the caller's own markers and the provider default.
func ephemeralPolicy() Value {
	value, _ := EncodeValue(map[string]any{"type": "ephemeral"})
	return value
}
