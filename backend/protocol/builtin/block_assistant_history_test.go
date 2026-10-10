package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestBlockHistoryKeepsThinkingTextAndCallsInOneTurn(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	raw := `{"model":"m","store":false,"max_output_tokens":32,"input":[{"role":"user","content":"hi"},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"visible"}]},{"role":"assistant","content":"before"},{"type":"function_call","call_id":"c1","name":"echo","arguments":"{}"},{"role":"assistant","content":"between"},{"type":"function_call","call_id":"c2","name":"echo","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"one"},{"type":"function_call_output","call_id":"c2","output":"two"}]}`
	r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			to := shippedProjectionProtocol(t, codec)
			policy := p.DefaultConversionPolicy(from, to)
			c, _ := p.ResolveConversion(policy)
			sink := &p.DiagnosticSink{}
			projected, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
			if err != nil {
				t.Fatal(err)
			}
			if len(projected.Content) != 4 || len(projected.Content[1].Children) != 5 {
				t.Fatal("turn split", projected.Content)
			}
			for i, kind := range []p.NodeKind{p.ReasoningNode, p.TextNode, p.ToolCallNode, p.TextNode, p.ToolCallNode} {
				if projected.Content[1].Children[i].Kind != kind {
					t.Fatal("block order changed")
				}
			}
			if len(r.Content) != 8 {
				t.Fatal("source changed")
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if err = to.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
				t.Fatal(err)
			}
			decoded, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded.Content[1].Children) != 5 {
				t.Fatal("wire split the turn")
			}
			definition := from.Definition()
			expected, _ := p.EncodeValue(r)
			definition.Samples = append(definition.Samples, p.Sample{ID: "mixed-block-history", Direction: p.DecodeRequest, Input: testValue(t, raw), Expected: expected, Scope: p.Scope{Model: "m"}, Capabilities: []p.Capability{p.TextCapability, p.FunctionToolsCapability, p.ReasoningCapability}})
			definitionWire, _ := p.EncodeValue(definition)
			compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
			withSample, issues := compiler.Compile(definitionWire.Bytes())
			if err = p.IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			report := p.VerifyBindingCombination(t.Context(), withSample, to, p.CapabilitySet{p.TextCapability: true, p.FunctionToolsCapability: true, p.ReasoningCapability: true, p.UsageCapability: true})
			verified := false
			for _, check := range report.Checks {
				if check.SampleID == "mixed-block-history" {
					verified = check.Passed && !check.Skipped
				}
			}
			if !verified {
				t.Fatal("mixed block history did not round-trip", report.Issues)
			}
			found := false
			for _, issue := range sink.Issues() {
				found = found || (issue.RuleID == "block-assistant-history" && issue.PolicyHash == c.Hash)
			}
			if !found {
				t.Fatal("grouping not diagnosed")
			}
			disabled := p.DefaultConversionPolicy(from, to)
			for i := range disabled.Rules {
				if disabled.Rules[i].Action == "assistant_history" {
					disabled.Rules[i].Enabled = false
				}
			}
			off, _ := p.ResolveConversion(disabled)
			unprojected, err := off.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = to.EncodeRequest(t.Context(), unprojected, p.EvaluationContext{}); err == nil {
				t.Fatal("disabled grouping hidden in encoder")
			}
			protected := r.Clone()
			protected.Content[2].Cache = []p.CacheIntent{{Kind: "breakpoint"}}
			if _, err = c.Request(t.Context(), protected, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil); err == nil {
				t.Fatal("message cache scope moved")
			}
			policy.Mode = "strict"
			c, _ = p.ResolveConversion(policy)
			if _, err = c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil); err == nil {
				t.Fatal("strict merged message boundaries")
			}
		})
	}
}
