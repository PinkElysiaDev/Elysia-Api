package protocol

import "testing"

func TestReasoningCanonicalizationRetainsPartsAndState(t *testing.T) {
	part := Node{Kind: TextNode, Payload: StringValue("full")}
	node := Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{part}}
	plain := CanonicalReasoning(node)
	if plain.ReasoningForm != "" || plain.Payload != part.Payload || plain.ReasoningContent != nil {
		t.Fatal(plain)
	}
	if ContinuationNodeDigest(node) != ContinuationNodeDigest(plain) {
		t.Fatal("equivalent single part changed continuation digest")
	}
	for _, extra := range []Node{
		{Kind: TextNode, Payload: StringValue("full"), Attributes: Object{"vendor": StringValue("state")}},
		{Kind: TextNode, Payload: StringValue("full"), Resources: []Resource{{Kind: "signature", ID: StringValue("sig")}}},
		{Kind: TextNode, Payload: StringValue("full"), ID: StringValue("part-id")},
	} {
		node.ReasoningContent = []Node{extra}
		if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
			t.Fatal("part fields discarded")
		}
	}
	node.ReasoningContent = []Node{part, part}
	if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
		t.Fatal("multiple parts merged")
	}
	node.ReasoningContent = []Node{part}
	node.Children = []Node{{Kind: TextNode, Payload: StringValue("brief")}}
	if CanonicalReasoning(node).ReasoningForm != StructuredReasoning {
		t.Fatal("summary discarded")
	}
	if ContinuationNodeDigest(node) == ContinuationNodeDigest(plain) {
		t.Fatal("mixed content authenticated as plain thinking")
	}
}

func TestStructuredReasoningReplayRejectsPartRewrites(t *testing.T) {
	for _, changed := range [][]Node{
		{{Kind: TextNode, Payload: StringValue("replacement")}}, {},
	} {
		replay, err := NewEventReplay(Target{Capabilities: CapabilitySet{ReasoningCapability: true, TextCapability: true}}, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("r"), Item: &Node{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("original")}}}}
		if _, err = replay.Consume(start); err != nil {
			t.Fatal(err)
		}
		changedEvent := start
		changedEvent.Type = ItemSnapshot
		node := *start.Item
		node.ReasoningContent = changed
		changedEvent.Item = &node
		if _, err = replay.Consume(changedEvent); err == nil {
			t.Fatal("emitted part changed")
		}
	}
}

func TestStructuredReasoningPartUsesResourceScopeAndEvidenceChecks(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("scoped-parts", "m", "h", "t", "r"))
	scope := Scope{Provider: "provider", Account: "account", Model: "model"}
	part := Node{Kind: TextNode, Payload: StringValue("text"), Resources: []Resource{{Kind: "signature", ID: StringValue("synthetic")}}, Native: &Native{Value: StringValue("native")}}
	request := &Request{Content: []Node{{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{part}}}}
	if !HasScopedResources(request) {
		t.Fatal("part resource bypassed account selection")
	}
	if err := stampResourceScopes(request, scope); err != nil {
		t.Fatal(err)
	}
	compiled.stampRequestProvenance(request, scope)
	got := request.Content[0].ReasoningContent[0]
	if got.Resources[0].Scope != scope || got.Source == nil || got.Source.Protocol != compiled.Identity() || got.Source.Scope != scope {
		t.Fatal("part provenance not stamped", got)
	}
	if !observeCapabilities(request).observed[SignaturesCapability] {
		t.Fatal("binding evidence missed part signature")
	}
	event := Event{Item: &request.Content[0]}
	if err := compiled.stampEventProvenance(&event, DecodeEvent, scope); err != nil {
		t.Fatal(err)
	}
	if event.Item.ReasoningContent[0].Source.Direction != DecodeEvent {
		t.Fatal("event part provenance not stamped")
	}
	request.Content[0].ReasoningContent[0].Resources[0].Scope.Account = "different-account"
	if err := stampResourceScopes(request, scope); err == nil {
		t.Fatal("nested part resource allowed account replacement")
	}
	limits := DefaultLimits()
	limits.Nodes = 1
	if issues := CheckToolAssociations(request.Content, limits); len(issues) == 0 || issues[0].Code != LimitExceeded {
		t.Fatal("part not included in history bounds", issues)
	}
}

func TestStructuredReasoningPartReceivesNamedNodeRules(t *testing.T) {
	policy := ConversionPolicy{SchemaVersion: 1, Rules: []ConversionRule{{ID: "part-edit", Enabled: true, Phase: ConversionRequest, Match: ConversionMatch{NodeKind: TextNode}, Action: "set", Path: "/payload", Value: StringValue("edited")}}}
	c, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	source := &Request{SchemaVersion: 1, Content: []Node{{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("original")}}}}}
	sink := &DiagnosticSink{}
	result, err := c.Request(t.Context(), source, ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content[0].ReasoningContent[0].Payload != StringValue("edited") || source.Content[0].ReasoningContent[0].Payload != StringValue("original") {
		t.Fatal("rule skipped part or changed source")
	}
	if len(sink.Issues()) == 0 || sink.Issues()[0].RuleID != "part-edit" || sink.Issues()[0].Path != "/payload" {
		t.Fatal("missing part diagnostic", sink.Issues())
	}
}
