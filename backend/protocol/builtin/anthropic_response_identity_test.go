package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageResponseIdentity(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	options, err := (p.EvaluationContext{}).WithOperation("fixture", 123)
	if err != nil {
		t.Fatal(err)
	}
	route := (p.ConversionContext{Source: p.Identity{Family: "gemini", WireVersion: "v2"}, Target: compiled.Identity()}).WithOperation(options)
	for _, direction := range []p.Direction{p.EncodeResponse, p.EncodeEvent} {
		rule := "response-shape"
		if direction == p.EncodeEvent {
			rule = "event-shape"
		}
		policy := declaredRulePolicy(t, compiled, compiled, rule, "strict")
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []p.Value{{}, p.StringValue("upstream"), testValue(t, "null"), p.StringValue("")} {
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: id, Model: p.StringValue("m"), Status: p.StringValue("completed"),
				Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}},
				Usage:   &p.Usage{Input: &p.Counter{Count: 1, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}}}
			expected := id
			if id.IsZero() {
				expected = p.StringValue("msg_fixture")
			}
			sink := &p.DiagnosticSink{}
			if direction == p.EncodeResponse {
				result, err := conversion.Response(t.Context(), response, route, sink)
				if err != nil || result.ID != expected || response.ID != id {
					t.Fatal("response identity was replaced or failed to use this execution", result, err)
				}
			} else {
				event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Response: response}
				result, err := conversion.Event(t.Context(), event, route, sink)
				if err != nil || result.Response.ID != expected || response.ID != id {
					t.Fatal("stream response identity was replaced or failed to use this execution", result, err)
				}
				if id.IsZero() && result.ResponseID != expected || !id.IsZero() && !result.ResponseID.IsZero() {
					t.Fatal("stream identity was assigned despite an existing source representation", result)
				}
			}
			if id.IsZero() {
				if issues := sink.Issues(); len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].RuleID != rule {
					t.Fatal("generated identity lost its declared normalization", issues)
				}
			} else if len(sink.Issues()) != 0 {
				t.Fatal("present identity was treated as missing", sink.Issues())
			}
		}
	}

	policy := declaredRulePolicy(t, compiled, compiled, "response-shape", "strict")
	policy.Rules[0].Enabled = false
	disabled, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Status: p.StringValue("completed"),
		Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}},
		Usage:   &p.Usage{Input: &p.Counter{Count: 1, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}}}
	unresolved, err := disabled.Response(t.Context(), response, route, nil)
	if err != nil || !unresolved.ID.IsZero() {
		t.Fatal("disabled identity rule still assigned an ID", unresolved, err)
	}
	wire, err := compiled.EncodeResponse(t.Context(), unresolved, options)
	if err == nil && compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))) == nil {
		t.Fatal("encoding replaced the disabled identity rule")
	}
}
