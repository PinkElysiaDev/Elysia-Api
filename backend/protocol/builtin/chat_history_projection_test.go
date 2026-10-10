package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestChatHistoryGroupsThinkingWithAssociatedReply(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	for _, tool := range []bool{false, true} {
		last := `{"role":"assistant","content":"answer"}`
		if tool {
			last = `{"type":"function_call","call_id":"call1","name":"echo","arguments":"{\"n\":9007199254740993}"},{"type":"function_call","call_id":"call2","name":"echo","arguments":"{}"}`
		}
		raw := `{"model":"m","store":false,"input":[{"role":"user","content":"hi"},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"visible"}]},` + last + `]}`
		r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		for _, strict := range []bool{false, true} {
			policy := p.DefaultConversionPolicy(from, to)
			if strict {
				policy.Mode = "strict"
			}
			c, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Content) < 3 || len(projected.Content) != 2 || projected.Content[1].Kind != p.MessageNode {
				t.Fatal("source changed or assistant turn not grouped")
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if err := to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
				t.Fatal(err)
			}
			var messages []p.Value
			obj, _ := testValue(t, string(wire)).ReadObject()
			_ = obj["messages"].Decode(&messages)
			assistant, _ := messages[1].ReadObject()
			if assistant["reasoning_content"] != p.StringValue("visible") {
				t.Fatal("thought lost")
			}
			if tool {
				if !strings.Contains(string(wire), "call1") || !strings.Contains(string(wire), "call2") || !strings.Contains(string(wire), "9007199254740993") {
					t.Fatal("tool association/precision lost")
				}
			} else if assistant["content"].IsZero() {
				t.Fatal("thinking-only message sent")
			}
			found := false
			for _, i := range sink.Issues() {
				found = found || (i.RuleID == "chat-assistant-history" && i.Code == p.ConversionNormalized && i.PolicyHash == c.Hash)
			}
			if !found {
				t.Fatal("normalization not diagnosed")
			}
		}
		policy := p.DefaultConversionPolicy(from, to)
		for i := range policy.Rules {
			if policy.Rules[i].Action == "chat_history" {
				policy.Rules[i].Enabled = false
			}
		}
		c, _ := p.ResolveConversion(policy)
		projected, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
		if err == nil {
			err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
		}
		if err == nil {
			t.Fatal("disabled rule silently grouped history")
		}
	}
}

func TestChatHistoryDoesNotCrossTurnsOrDropProtectedState(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	base := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.ReasoningNode, Payload: p.StringValue("visible")}, {Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("reply")}}}}}
	for _, mutate := range []func(*p.Request){
		func(r *p.Request) { r.Content[1].Role = p.StringValue("user") },
		func(r *p.Request) { r.Content = r.Content[:1] },
		func(r *p.Request) { r.Content[1].Cache = []p.CacheIntent{{Kind: "breakpoint"}} },
		func(r *p.Request) { r.Content[0].ID = p.StringValue("rs") },
	} {
		r := base.Clone()
		mutate(r)
		policy := p.DefaultConversionPolicy(from, to)
		policy.Mode = "strict"
		c, _ := p.ResolveConversion(policy)
		if _, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil); err == nil {
			t.Fatal("unsafe history accepted")
		}
	}
}
