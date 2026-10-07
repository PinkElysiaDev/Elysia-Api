package builtin

import p "github.com/elysia-api/backend/protocol"

// The provider documents one ordering constraint on mixed-TTL breakpoints:
// "Cache entries with longer TTL must appear before shorter TTLs." Blocks are
// processed tools -> system -> messages, so "before" means earlier in that walk,
// not earlier in JSON key order. A breakpoint with no explicit ttl is the
// provider default, 5m — the subtle case the rule is easy to violate with.
//
// The constraint is reported as a non-blocking diagnostic rather than a wire
// error: no reference implementation enforces it and the provider does not
// document the failing status, so refusing the request would 4xx a caller whose
// cache layout is merely non-optimal, suppressing the caching this path exists
// to preserve. The caller still gets the signal, upstream still sees the markers.

// breakpointTTLRank orders TTLs for the mixed-TTL walking rule: a longer TTL has
// a lower rank and should appear no later than a shorter one in the tools ->
// system -> messages walk. An unset or unrecognized TTL is treated as the 5m
// default.
func breakpointTTLRank(intent p.CacheIntent) int {
	if intent.TTL.IsZero() || intent.TTL.IsNull() {
		return 1 // provider default is 5m
	}
	var ttl string
	if err := intent.TTL.Decode(&ttl); err != nil {
		return 1
	}
	if ttl == "1h" {
		return 0
	}
	return 1
}

// breakpointTTL returns an explicit ttl string when the intent carries one.
func breakpointTTL(intent p.CacheIntent) (string, bool) {
	if intent.TTL.IsZero() || intent.TTL.IsNull() {
		return "", false
	}
	var ttl string
	if err := intent.TTL.Decode(&ttl); err != nil {
		return "", false
	}
	return ttl, true
}

// warnBreakpointOrder walks every breakpoint in the provider's processing order
// (tools, then content, then the top-level request marker) and records a warning
// where a shorter-TTL marker precedes a longer-TTL one, or where a ttl falls
// outside the provider's known set. The check lives on the ordered stream because
// encodeCache can only ever observe a single intent; the conflict is between
// markers in different sections.
func (adapter module) warnBreakpointOrder(request *p.Request, sink *p.DiagnosticSink) {
	if sink == nil {
		return
	}
	seenRank := -1 // below any real rank, so the first marker always passes
	visit := func(intents []p.CacheIntent) {
		for _, intent := range intents {
			if intent.Kind != "breakpoint" {
				continue
			}
			if ttl, ok := breakpointTTL(intent); ok && ttl != "1h" && ttl != "5m" {
				sink.Add(p.ConversionIssue{Code: p.InvalidInput, Severity: p.SeverityWarning, Stage: "wire", Path: "/cache",
					Reason:     "unrecognized cache breakpoint ttl: " + ttl,
					Suggestion: "Use 1h or 5m, or omit ttl for the provider's 5m default."})
			}
			rank := breakpointTTLRank(intent)
			if rank < seenRank {
				sink.Add(p.ConversionIssue{Code: p.InvalidInput, Severity: p.SeverityWarning, Stage: "wire", Path: "/cache",
					Reason:     "longer cache TTL must precede shorter: a 1h breakpoint follows a 5m one",
					Suggestion: "Move the 1h marker before every 5m marker in the provider's tools, system, then messages order."})
			}
			seenRank = rank
		}
	}
	for index := range request.Tools {
		visit(request.Tools[index].Cache)
	}
	var walk func([]p.Node)
	walk = func(nodes []p.Node) {
		for index := range nodes {
			visit(nodes[index].Cache)
			walk(nodes[index].Children)
		}
	}
	walk(request.Content)
	visit(request.Cache)
}

func decodeCache(fields p.Object, location string) ([]p.CacheIntent, error) {
	value := fields["cache_control"]
	if value.IsZero() {
		return nil, nil
	}
	intent := p.CacheIntent{Kind: "breakpoint", Location: location, Value: value}
	if !value.IsNull() {
		policy, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		intent.TTL = policy["ttl"]
		delete(policy, "ttl")
		intent.Value = object(policy)
	}
	return []p.CacheIntent{intent}, nil
}

// encodeCache writes an Anthropic cache_control policy onto one wire node. The
// gate is the declared capability, not the protocol name: CheckRequest refuses a
// breakpoint whose target does not declare cache.breakpoints before any encoder
// runs, so a module that reaches this point has either the built-in Anthropic
// mapping or a custom definition that opted in by declaring the capability. Every
// call site shares this one writer; none of them repeat the capability check.
func (adapter module) encodeCache(fields p.Object, intents []p.CacheIntent) error {
	if len(intents) == 0 {
		return nil
	}
	if len(intents) > 1 {
		return unsupported("/cache", "one wire cache_control cannot express multiple policies")
	}
	intent := intents[0]
	if intent.Kind != "breakpoint" {
		return unsupported("/cache", "this node accepts only cache breakpoints")
	}
	value := intent.Value
	if !value.IsNull() {
		policy, err := value.ReadObject()
		if err != nil {
			return err
		}
		// TTL has one semantic owner; deleting it must not revive a native value.
		policy["ttl"] = intent.TTL
		value = object(policy)
	}
	fields["cache_control"] = value
	return nil
}
