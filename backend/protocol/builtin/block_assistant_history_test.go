package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestBlockHistoryKeepsThinkingTextAndCallsInOneTurn(t *testing.T) {
	const raw = `{"model":"m","store":false,"max_output_tokens":32,"input":[{"role":"user","content":"hi"},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"visible"}]},{"role":"assistant","content":"before"},{"type":"function_call","call_id":"c1","name":"echo","arguments":"{}"},{"role":"assistant","content":"between"},{"type":"function_call","call_id":"c2","name":"echo","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"one"},{"type":"function_call_output","call_id":"c2","output":"two"}]}`
	from := testCompiled(t, Responses)
	for _, hasIdentity := range []bool{false, true} {
		body := raw
		if hasIdentity {
			body = strings.Replace(body, `"role":"assistant","content":"before"`, `"type":"message","id":"message-1","status":"completed","role":"assistant","content":"before"`, 1)
		}
		request, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{Anthropic, Gemini} {
			target := testCompiled(t, name)
			if !hasIdentity {
				verifyRequestEvidence(t, from, target, "mixed-block-history", raw, p.CapabilitySet{p.TextCapability: true, p.FunctionToolsCapability: true, p.ReasoningCapability: true, p.UsageCapability: true})
			}
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				t.Run(fmt.Sprintf("%s/%s/identity=%v", name, mode, hasIdentity), func(t *testing.T) {
					policy := p.DefaultConversionPolicy(from, target)
					if mode == "strict" {
						policy.Mode = mode
					}
					if mode == "disabled" {
						for index := range policy.Rules {
							if policy.Rules[index].ID == "assistant-history" {
								policy.Rules[index].Enabled = false
							}
						}
					}
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					route := p.ConversionContext{Source: from.Identity(), Target: target.Identity()}
					projected, err := conversion.Request(t.Context(), request, route, sink)
					if mode == "strict" {
						if err == nil {
							t.Fatal("strict accepted changed assistant boundaries")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wire, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
					if mode == "disabled" {
						if err == nil {
							t.Fatal("encoder hid disabled history grouping")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(projected.Content) != 4 || len(projected.Content[1].Children) != 5 || len(request.Content) != 8 {
						t.Fatal("assistant turn was split or source mutated", projected.Content)
					}
					for index, kind := range []p.NodeKind{p.ReasoningNode, p.TextNode, p.ToolCallNode, p.TextNode, p.ToolCallNode} {
						if projected.Content[1].Children[index].Kind != kind {
							t.Fatal("assistant block order changed")
						}
					}
					decoded, err := target.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
					if err != nil || len(decoded.Content[1].Children) != 5 {
						t.Fatal("target wire split the assistant turn", string(wire), err)
					}
					hasGrouping := false
					for _, issue := range sink.Issues() {
						hasGrouping = hasGrouping || issue.RuleID == "assistant-history" && issue.PolicyHash == conversion.Hash
					}
					if !hasGrouping {
						t.Fatal("history grouping lost its diagnostic", sink.Issues())
					}
					protected := request.Clone()
					protected.Content[2].Cache = []p.CacheIntent{{Kind: "breakpoint"}}
					if _, err := conversion.Request(t.Context(), protected, route, nil); err == nil {
						t.Fatal("message cache scope moved during grouping")
					}
				})
			}
		}
	}
}
