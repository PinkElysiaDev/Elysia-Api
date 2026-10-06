package protocol

import "testing"

func breakpointCapabilities() CapabilitySet {
	return CapabilitySet{CacheBreakpointsCapability: true}
}

func textNode(text string) Node {
	return Node{Kind: TextNode, Payload: StringValue(text)}
}

func systemMessage(text string) Node {
	return Node{Kind: MessageNode, Role: StringValue("system"), Children: []Node{textNode(text)}}
}

func userMessage(text string) Node {
	return Node{Kind: MessageNode, Role: StringValue("user"), Children: []Node{textNode(text)}}
}

func toolNode(name string) Tool {
	return Tool{Kind: FunctionTool, Name: StringValue(name), InputSchema: StringValue(`{"type":"object"}`)}
}

func testRequest(content []Node, tools []Tool) *Request {
	return &Request{SchemaVersion: SemanticSchemaVersion, Model: StringValue("m"), Content: content, Tools: tools}
}

// Synthesis is opt-in by capability: a target that does not declare
// cache.breakpoints must receive no markers, because the encoder would reject
// them or leak a foreign field onto the wire.
func TestSynthesizeCacheBreakpointsRequiresTargetCapability(t *testing.T) {
	request := testRequest([]Node{systemMessage("sys"), userMessage("hi")}, nil)
	if got := SynthesizeCacheBreakpoints(request, CapabilitySet{}, EvaluationContext{}); got != 0 {
		t.Fatalf("placed %d breakpoints without the capability", got)
	}
	if countCacheBreakpoints(request) != 0 {
		t.Fatal("a declined target must not be modified")
	}
}

// The three structural anchors follow the provider's prefix hierarchy:
// last tool, last system block, and the message tail.
func TestSynthesizeCacheBreakpointsPlacesStructuralAnchors(t *testing.T) {
	request := testRequest(
		[]Node{systemMessage("sys"), userMessage("hi")},
		[]Tool{toolNode("first"), toolNode("second")},
	)
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), EvaluationContext{}); got != 3 {
		t.Fatalf("expected 3 anchors, placed %d", got)
	}
	if len(request.Tools[0].Cache) != 0 {
		t.Fatal("the first tool is not a stable anchor")
	}
	if len(request.Tools[1].Cache) != 1 {
		t.Fatal("the last tool must carry the tools anchor")
	}
	if len(request.Content[0].Children[0].Cache) != 1 {
		t.Fatal("the last system block must carry the system anchor")
	}
	if len(request.Content[1].Children[0].Cache) != 1 {
		t.Fatal("the message tail must carry the conversation anchor")
	}
	// A marker on the message envelope would be dropped by block-level encoding.
	if len(request.Content[0].Cache) != 0 || len(request.Content[1].Cache) != 0 {
		t.Fatal("markers belong to blocks, never to message envelopes")
	}
}

// A caller marker owns its slot. Synthesis must never overwrite, reorder or
// remove one, and must spend the remaining budget elsewhere.
func TestSynthesizeCacheBreakpointsPreservesCallerMarkers(t *testing.T) {
	callerPolicy, _ := EncodeValue(map[string]any{"type": "ephemeral", "ttl": "1h"})
	request := testRequest(
		[]Node{systemMessage("sys"), userMessage("hi")},
		[]Tool{toolNode("only")},
	)
	request.Content[0].Children[0].Cache = []CacheIntent{{Kind: "breakpoint", Location: "block", Value: callerPolicy, TTL: StringValue("1h")}}
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), EvaluationContext{}); got != 2 {
		t.Fatalf("the occupied slot must be skipped, placed %d", got)
	}
	kept := request.Content[0].Children[0].Cache[0]
	if string(kept.TTL.Bytes()) != `"1h"` {
		t.Fatalf("caller TTL was edited: %s", kept.TTL.Bytes())
	}
	if len(request.Tools[0].Cache) != 1 || len(request.Content[1].Children[0].Cache) != 1 {
		t.Fatal("remaining slots must still be filled")
	}
}

// The provider accepts four explicit breakpoints. A caller already at or above
// the limit is left exactly as sent; the excess is diagnosed, never trimmed.
func TestSynthesizeCacheBreakpointsStopsAtProviderLimit(t *testing.T) {
	request := testRequest([]Node{userMessage("hi")}, nil)
	for index := 0; index < 4; index++ {
		request.Content[0].Children[0].Cache = append(request.Content[0].Children[0].Cache, CacheIntent{Kind: "breakpoint", Location: "block", Value: ephemeralPolicy()})
	}
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), EvaluationContext{}); got != 0 {
		t.Fatalf("no budget remains, placed %d", got)
	}
	sink := &DiagnosticSink{}
	options := EvaluationContext{Diagnostics: sink}
	request.Content[0].Children[0].Cache = append(request.Content[0].Children[0].Cache, CacheIntent{Kind: "breakpoint", Location: "block", Value: ephemeralPolicy()})
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), options); got != 0 {
		t.Fatalf("an over-limit caller must not receive more, placed %d", got)
	}
	issues := sink.Issues()
	if len(issues) != 1 || issues[0].Severity != SeverityWarning || issues[0].Stage != "synthesis" {
		t.Fatalf("over-limit callers must be diagnosed: %+v", issues)
	}
	if countCacheBreakpoints(request) != 5 {
		t.Fatal("an over-limit caller must be left untouched")
	}
}

// Reasoning blocks carry per-turn provider state, so a boundary there would
// never match a later prefix. A message with no cacheable child yields nothing.
func TestSynthesizeCacheBreakpointsSkipsReasoningAndEmptyEnvelopes(t *testing.T) {
	request := testRequest(
		[]Node{
			{Kind: MessageNode, Role: StringValue("system")},
			{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: ReasoningNode, Payload: StringValue("think")}}},
		},
		nil,
	)
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), EvaluationContext{}); got != 0 {
		t.Fatalf("no cacheable anchor exists, placed %d", got)
	}
}

// The budget spans the whole request: markers the caller placed in any section
// reduce the room left for structural anchors, which are then spent in prefix
// order (tools, then system, then conversation) until the budget is gone.
func TestSynthesizeCacheBreakpointsSpendsOneSharedBudget(t *testing.T) {
	request := testRequest(
		[]Node{systemMessage("sys"), userMessage("hi")},
		[]Tool{toolNode("only")},
	)
	request.Cache = []CacheIntent{{Kind: "breakpoint", Location: "request", Value: ephemeralPolicy()}}
	for index := 0; index < 2; index++ {
		request.Tools[0].Cache = append(request.Tools[0].Cache, CacheIntent{Kind: "breakpoint", Location: "tool", Value: ephemeralPolicy()})
	}
	if got := SynthesizeCacheBreakpoints(request, breakpointCapabilities(), EvaluationContext{}); got != 1 {
		t.Fatalf("only one slot remains, placed %d", got)
	}
	if len(request.Content[0].Children[0].Cache) != 1 {
		t.Fatal("the remaining slot belongs to the earlier system anchor")
	}
	if len(request.Content[1].Children[0].Cache) != 0 {
		t.Fatal("the conversation anchor must not exceed the shared budget")
	}
}
