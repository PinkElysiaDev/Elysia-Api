package protocol

import "testing"

func TestDeclaredTextLayoutKeepsOrderAndOffsets(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "text-layout", Order: 250, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{
		Operation: "merge_text", Path: "/content/*/children", PathBinding: "position",
		When: Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression("text")}},
		Text: &ProtocolTextLayout{ShouldCross: true, Measure: Expression{Op: "utf16_length", Source: expressionPointer(readExpression("input", "/payload"))}, OffsetBinding: "offset", LengthBinding: "length", Metadata: map[string]Expression{
			"range": {Op: "object", Fields: map[string]Expression{"model": readExpression("context", "/model"), "offset": {Op: "read", From: "binding", Name: "offset"}, "length": {Op: "read", From: "binding", Name: "length"}, "value": readExpression("input", "/value")}},
		}},
		Steps: []ProtocolControlStep{{Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Source: expressionPointer(Expression{Op: "read", From: "binding", Name: "position"}), Reason: literalExpression("target merges text boundaries")}}},
	}}}}}
	compiled := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{
		{Kind: TextNode, Payload: StringValue("😀"), Native: &Native{Value: fixtureValue(t, Object{})}},
		{Kind: ToolCallNode, CallID: StringValue("call"), Name: StringValue("f"), Input: &ToolInput{Kind: JSONInput, Value: fixtureValue(t, Object{})}},
		{Kind: TextNode, Payload: StringValue("hi"), Metadata: []ResponseMetadata{{Name: "range", Location: "content", Codec: "custom", Value: fixtureValue(t, Object{"start": coreInteger(0)})}}},
	}}}}
	before := fixtureValue(t, response)
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: "m"}
	result, err := conversion.Response(t.Context(), response, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	children := result.Content[0].Children
	if len(children) != 2 || !children[0].Payload.IsString("😀hi") || children[0].Native != nil || children[1].Kind != ToolCallNode {
		t.Fatal("text layout changed non-text order or replayed old text", children)
	}
	if !equalValues(children[0].Metadata[0].Value, fixtureValue(t, Object{"model": StringValue("m"), "offset": coreInteger(2), "length": coreInteger(2), "value": fixtureValue(t, Object{"start": coreInteger(0)})})) || fixtureValue(t, response) != before {
		t.Fatal("text offset or input ownership changed", children[0].Metadata)
	}
	policy.Mode = "strict"
	strict, _ := ResolveConversion(policy)
	if _, err := strict.Response(t.Context(), response, route, nil); err == nil {
		t.Fatal("strict mode accepted text boundary loss")
	}
	response.Content[0].Children[2].Resources = []Resource{{Kind: "signature", ID: StringValue("protected")}}
	if _, err := conversion.Response(t.Context(), response, route, nil); err == nil {
		t.Fatal("text layout consumed protected state")
	}
	response.Content[0].Children[2].Resources = nil
	response.Content[0].Children[2].Metadata[0].Name = "unknown"
	if _, err := conversion.Response(t.Context(), response, route, nil); err == nil {
		t.Fatal("text layout dropped undeclared metadata")
	}
	response.Content[0].Children = response.Content[0].Children[:1]
	response.Content[0].Children[0].Payload = fixtureValue(t, nil)
	sink := &DiagnosticSink{}
	unchanged, err := conversion.Response(t.Context(), response, route, sink)
	if err != nil || len(sink.Issues()) != 0 || unchanged.Content[0].Children[0].Native == nil || !unchanged.Content[0].Children[0].Payload.IsNull() {
		t.Fatal("single leaf was materialized as a merge", err, sink.Issues())
	}
}

func TestUTF16LengthExpression(t *testing.T) {
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "utf16_length", Source: expressionPointer(readExpression("input", "/text"))}))
	for _, fixture := range []struct {
		text string
		want int64
	}{{"", 0}, {"a界😀", 4}} {
		output, err := plan.evaluate(evaluation{ctx: t.Context(), input: fixtureValue(t, Object{"text": StringValue(fixture.text)}), budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil || output != coreInteger(fixture.want) {
			t.Fatal(output, err)
		}
	}
}
