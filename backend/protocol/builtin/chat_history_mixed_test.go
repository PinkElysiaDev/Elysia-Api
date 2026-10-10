package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const mixedToolHistory = `{"model":"m","store":false,"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"call1","name":"echo","arguments":"{\"n\":9007199254740993}"},{"role":"assistant","content":"between"},{"type":"function_call","call_id":"call2","name":"echo","arguments":"{}"},{"role":"assistant","content":"after"},{"type":"function_call_output","call_id":"call1","output":"one"},{"type":"function_call_output","call_id":"call2","output":"two"}]}`

func TestChatHistoryKeepsToolResultsAdjacentAfterMixedItems(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	r, err := from.DecodeRequest(t.Context(), []byte(mixedToolHistory), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity()}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
	sink := &p.DiagnosticSink{}
	projected, err := c.Request(t.Context(), r, route, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 7 || r.Content[1].Kind != p.ToolCallNode {
		t.Fatal("source history changed")
	}
	if len(projected.Content) != 4 {
		t.Fatal("assistant turn not grouped")
	}
	children := projected.Content[1].Children
	if len(children) != 4 || children[0].Payload != p.StringValue("between") || children[1].Payload != p.StringValue("after") || children[2].CallID != p.StringValue("call1") || children[3].CallID != p.StringValue("call2") {
		t.Fatal("projection lost text or tool associations", children)
	}
	var boundary, interleaving bool
	for _, issue := range sink.Issues() {
		if issue.RuleID == "chat-assistant-history" && issue.PolicyHash == c.Hash && issue.Severity == p.SeverityWarning {
			boundary = boundary || strings.Contains(issue.Reason, "message boundaries")
			interleaving = interleaving || strings.Contains(issue.Reason, "interleaving")
		}
	}
	if !boundary || !interleaving {
		t.Fatal("losses not diagnosed", sink.Issues())
	}
	wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), "9007199254740993") {
		t.Fatal("argument precision lost")
	}
	decoded, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range decoded.Content[1].Children {
		if n.Kind != children[i].Kind || n.Payload != children[i].Payload || n.CallID != children[i].CallID {
			t.Fatal("wire reordered projected semantics")
		}
	}
	for _, mode := range []string{"strict", "disabled", "protected"} {
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
			case "protected":
				input.Content[2].Cache = []p.CacheIntent{{Kind: "breakpoint"}}
			}
			c, _ := p.ResolveConversion(policy)
			out, err := c.Request(t.Context(), input, route, nil)
			if err == nil {
				var wire []byte
				wire, err = to.EncodeRequest(t.Context(), out, p.EvaluationContext{})
				if err == nil {
					err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
				}
			}
			if err == nil {
				t.Fatal("unsafe history accepted")
			}
		})
	}
}

func TestChatMixedHistoryCombinationUsesProjectedOrder(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	decoded, err := from.DecodeRequest(t.Context(), []byte(mixedToolHistory), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := p.EncodeValue(decoded)
	definition := from.Definition()
	definition.Samples = append(definition.Samples, p.Sample{ID: "mixed-tool-history", Direction: p.DecodeRequest, Input: testValue(t, mixedToolHistory), Expected: expected, Capabilities: []p.Capability{p.TextCapability, p.FunctionToolsCapability}})
	raw, err := p.EncodeValue(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	from, issues := compiler.Compile(raw.Bytes())
	if err = p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	report := p.VerifyBindingCombination(t.Context(), from, to, p.CapabilitySet{p.TextCapability: true, p.FunctionToolsCapability: true, p.UsageCapability: true})
	for _, check := range report.Checks {
		if check.SampleID == "mixed-tool-history" {
			if !check.Passed || check.Skipped {
				t.Fatal(check, report.Issues)
			}
			return
		}
	}
	t.Fatal("mixed history was not verified", report)
}

func TestChatFinalWireRejectsBrokenToolSequence(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	call := `{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"echo","arguments":"{}"}}]}`
	result := `{"role":"tool","tool_call_id":"c","content":"done"}`
	for _, messages := range []string{call, call + `,{"role":"assistant","content":"interruption"},` + result, call + `,{"role":"user","content":"interruption"},` + result, call + `,` + result + `,` + result, result} {
		if err := chat.ValidateWireOutput(p.EncodeRequest, testValue(t, `{"model":"m","messages":[`+messages+`]}`)); err == nil {
			t.Fatal("invalid sequence accepted", messages)
		}
	}
	raw := `{"model":"m","messages":[{"role":"assistant","content":"first"},` + call + `,` + result + `,{"role":"assistant","content":"last"}]}`
	request, err := chat.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(chat, chat))
	request, err = c.Request(t.Context(), request, p.ConversionContext{Source: chat.Identity(), Target: chat.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := chat.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, raw)
	if err = chat.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
}

func TestChatHistoryProjectsNestedInterleaving(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Chat)
	raw := `{"model":"m","max_tokens":32,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"echo","input":{}},{"type":"text","text":"between"},{"type":"tool_use","id":"c2","name":"echo","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"one"},{"type":"tool_result","tool_use_id":"c2","content":"two"}]}]}`
	r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			policy := p.DefaultConversionPolicy(from, to)
			if mode == "strict" {
				policy.Mode = "strict"
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].Action == "chat_history" {
						policy.Rules[i].Enabled = false
					}
				}
			}
			c, _ := p.ResolveConversion(policy)
			projected, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
			if err == nil {
				var wire []byte
				wire, err = to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
				if err == nil {
					err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
				}
			}
			if (err == nil) != (mode == "compatible") {
				t.Fatal(mode, err)
			}
			if mode == "compatible" && (projected.Content[0].Children[0].Payload != p.StringValue("between") || r.Content[0].Children[0].Kind != p.ToolCallNode) {
				t.Fatal("source changed or text lost")
			}
		})
	}
}
