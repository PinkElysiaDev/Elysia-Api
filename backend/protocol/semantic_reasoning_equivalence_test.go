package protocol

import "testing"

func TestThinkingEnvelopeEquivalencePreservesRolesAndMetadata(t *testing.T) {
	thought := Node{Kind: ReasoningNode, Payload: StringValue("visible")}
	message := Node{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{thought}}
	if got := comparableConversation([]Node{message}); len(got) != 1 || got[0].Kind != ReasoningNode {
		t.Fatal("protocol role envelope not normalized")
	}
	for _, mutate := range []func(*Node){
		func(n *Node) { n.Role = StringValue("user") },
		func(n *Node) { n.ID = StringValue("message-id") },
		func(n *Node) { n.Cache = []CacheIntent{{Kind: "breakpoint"}} },
		func(n *Node) { n.Metadata = []ResponseMetadata{{Name: "citations"}} },
	} {
		n := message
		mutate(&n)
		if got := comparableConversation([]Node{n}); len(got) != 1 || got[0].Kind != MessageNode {
			t.Fatal("meaningful message wrapper discarded")
		}
	}
	summary := Node{Kind: ReasoningNode, ReasoningForm: SummaryReasoning, Children: []Node{{Kind: TextNode, Payload: StringValue("visible")}}}
	a, _ := comparableSemantic(equivalentRequest(&Request{Content: []Node{thought}}))
	b, _ := comparableSemantic(equivalentRequest(&Request{Content: []Node{summary}}))
	if a == b {
		t.Fatal("summary confused with visible thought")
	}
}
