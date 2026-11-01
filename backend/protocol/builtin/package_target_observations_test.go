package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageBareHistory(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	text := func(value string) p.Node { return p.Node{Kind: p.TextNode, Payload: p.StringValue(value)} }
	request := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{text("hi")}},
		{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}},
		{Kind: p.ToolResultNode, CallID: p.StringValue("c"), Payload: p.StringValue("ok")},
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{text("answer")}},
		{Kind: p.ReasoningNode, Payload: p.StringValue("think")}, text("last"),
	}}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"type":"function","id":"c","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}]},{"role":"tool","tool_call_id":"c","content":"ok"},{"role":"assistant","content":"answer"},{"role":"assistant","content":[{"type":"text","text":"last"}],"reasoning_content":"think"}]}`)
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("grouping changed its source", err)
	}
	for _, node := range []p.Node{
		{Kind: p.ImageNode, Payload: testValue(t, `{"url":"https://example.test/image.png"}`)},
		{Kind: p.ReasoningNode, Payload: p.StringValue("protected"), Resources: []p.Resource{{Kind: "signature", ID: p.StringValue("signature"), Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}}},
	} {
		candidate := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: []p.Node{node}}
		if _, err := compiled.EncodeRequest(t.Context(), candidate, p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}); err == nil {
			t.Fatal("unowned or protected content was silently made an assistant message", node)
		}
	}
}

func TestChatPackageItemDisposition(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	response := &p.Response{SchemaVersion: target.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Attributes: p.Object{"created_at": testValue(t, `1`), "finishReason": p.StringValue("stop")}, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), ID: p.StringValue("message"), Status: p.StringValue("completed"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi"), ID: p.StringValue("part")}}}}}
	for _, mode := range []string{"compatible", "strict"} {
		policy := declaredRulePolicy(t, target, source, "response-shape", mode)
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.HTTPJSON}
		sink := &p.DiagnosticSink{}
		projected, err := conversion.Response(t.Context(), response, route, sink)
		if mode == "strict" {
			var failure *p.ConversionError
			if !errors.As(err, &failure) || failure.Issues[0].Code != p.ConversionRejected || failure.Issues[0].RuleID != "response-shape" {
				t.Fatal("strict item identity loss was not attributed", err)
			}
			continue
		}
		if err != nil || !projected.Content[0].ID.IsZero() || !projected.Content[0].Status.IsZero() || !projected.Content[0].Children[0].ID.IsZero() || len(sink.Issues()) != 3 {
			t.Fatal("declared item disposition lost content or diagnostics", projected, sink.Issues(), err)
		}
		wire, err := target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if err := target.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
			t.Fatal(err)
		}
		policy.Rules[0].Enabled = false
		disabled, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := disabled.Response(t.Context(), response, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.EncodeResponse(t.Context(), retained, p.EvaluationContext{}); err == nil {
			t.Fatal("encoder repeated disabled item disposition")
		}
	}
	if !response.Content[0].ID.IsString("message") || !response.Content[0].Children[0].ID.IsString("part") {
		t.Fatal("projection mutated source item identities")
	}
}

func TestResponsesPackageGeneratedJSONIdentities(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-chat"), compileBuiltinPackage(t, "openai-responses")
	options, err := (p.EvaluationContext{}).WithOperation("execution", 123)
	if err != nil {
		t.Fatal(err)
	}
	route := (p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.HTTPJSON}).WithOperation(options)
	response := &p.Response{SchemaVersion: target.CoreVersion(), Model: p.StringValue("m"), Status: p.StringValue("completed"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi")}}}}}
	for _, mode := range []string{"compatible", "strict"} {
		conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "response-shape", mode))
		if err != nil {
			t.Fatal(err)
		}
		sink := &p.DiagnosticSink{}
		projected, err := conversion.Response(t.Context(), response, route, sink)
		if err != nil {
			t.Fatal(err)
		}
		if !projected.ID.IsString("resp_execution") || !projected.Content[0].ID.IsString("msg_execution/content/0") || !projected.Content[0].Status.IsString("completed") || projected.Attributes["created_at"] != testValue(t, `123`) {
			t.Fatal("target identities did not bind the execution and content position", projected)
		}
		for _, issue := range sink.Issues() {
			if issue.Code != p.ConversionNormalized || issue.Fidelity != "preserved" {
				t.Fatal("target-generated fields were classified as observed or lossy", issue)
			}
		}
		wire, err := target.EncodeResponse(t.Context(), projected, options)
		if err != nil {
			t.Fatal(err)
		}
		if err := target.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
			t.Fatal(err)
		}
		repeated, err := conversion.Response(t.Context(), response, route, nil)
		if err != nil || repeated.Content[0].ID != projected.Content[0].ID {
			t.Fatal("one execution assigned unstable item identities", err)
		}
		for _, invalid := range []string{`null`, `""`, `3`} {
			candidate := *response
			candidate.Content = append([]p.Node(nil), response.Content...)
			candidate.Content[0].ID = testValue(t, invalid)
			result, err := conversion.Response(t.Context(), &candidate, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Content[0].ID != candidate.Content[0].ID {
				t.Fatal("existing invalid identity was replaced")
			}
			encoded, err := target.EncodeResponse(t.Context(), result, options)
			if err == nil {
				err = target.ValidateWireOutput(p.EncodeResponse, testValue(t, string(encoded)))
			}
			if err == nil {
				t.Fatal("invalid explicit identity passed the final contract", invalid)
			}
		}
	}
	if !response.ID.IsZero() || !response.Content[0].ID.IsZero() || response.Attributes != nil {
		t.Fatal("target projection changed the caller response")
	}
	failure, err := source.DecodeResponse(t.Context(), []byte(`{"error":{"type":"invalid_request_error","code":"invalid_request_error","message":"bad request"}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	conversion, err := p.ResolveConversion(declaredRulePolicy(t, target, source, "response-shape", "compatible"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Response(t.Context(), failure, route, sink)
	if err != nil || !projected.ID.IsZero() || !projected.Attributes["created_at"].IsZero() || len(sink.Issues()) != 0 {
		t.Fatal("HTTP error envelope acquired a generation identity or timestamp", projected, sink.Issues(), err)
	}
	wire, err := target.EncodeResponse(t.Context(), projected, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	policy, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		t.Fatal(err)
	}
	// JSON fixtures retain their own transport inside a stream model binding.
	report := p.VerifyBindingCombination(t.Context(), target, source, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true},
		policy.WithVerificationContext(p.ConversionContext{Transport: p.SSE, Operation: "stream"}))
	hasJSONProof := false
	for _, check := range report.Checks {
		if check.SampleID == "text-decode-response" && check.Direction == p.EncodeResponse {
			hasJSONProof = check.Passed
		}
	}
	if !hasJSONProof {
		t.Fatal("stream binding skipped the JSON identity projection", report.Issues)
	}
}
