package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestChatHistoryJoinsOnlyAdjacentPlainVisibleThinking(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Chat)
	raw := `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"thought":true,"text":"one "},{"thought":true,"text":"two"},{"text":"answer"}]},{"role":"user","parts":[{"text":"continue"}]}]}`
	r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Model = p.StringValue("m")
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity()}
	for _, mode := range []string{"compatible", "strict", "disabled", "scoped", "interleaved", "summary"} {
		t.Run(mode, func(t *testing.T) {
			policy := p.DefaultConversionPolicy(from, to)
			input := r.Clone()
			switch mode {
			case "strict":
				policy.Mode = "strict"
			case "disabled":
				for i := range policy.Rules {
					if policy.Rules[i].Action == "chat_history" {
						policy.Rules[i].Enabled = false
					}
				}
			case "scoped":
				input.Content[1].Children[1].ID = p.StringValue("independent-item")
			case "interleaved":
				a := input.Content[1].Children
				a[1], a[2] = a[2], a[1]
			case "summary":
				input.Content[1].Children[1].ReasoningForm = p.SummaryReasoning
			}
			c, _ := p.ResolveConversion(policy)
			sink := &p.DiagnosticSink{}
			projected, err := c.Request(t.Context(), input, route, sink)
			if err == nil {
				var wire []byte
				wire, err = to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
				if err == nil {
					err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
				}
			}
			if mode != "compatible" {
				if err == nil {
					t.Fatal("unsafe join accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			children := projected.Content[1].Children
			if len(children) != 2 || children[0].Payload != p.StringValue("one two") || children[1].Payload != p.StringValue("answer") || len(r.Content[1].Children) != 3 {
				t.Fatal("text or source history changed")
			}
			found := false
			for _, issue := range sink.Issues() {
				found = found || (issue.RuleID == "chat-assistant-history" && issue.Severity == p.SeverityWarning && issue.PolicyHash == c.Hash)
			}
			if !found {
				t.Fatal("block loss not diagnosed")
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Content[1].Children[0].Payload != children[0].Payload {
				t.Fatal("projected reasoning changed on wire")
			}
		})
	}
}
