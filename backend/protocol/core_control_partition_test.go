package protocol

import "testing"

func TestDeclaredChildPartitionKeepsMessageOwners(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "child-order", Order: 230, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{
		Operation: "stable_partition", Path: "/content/*/children", PathBinding: "position", MovedBinding: "moved",
		When: Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression("text")}},
		Steps: []ProtocolControlStep{{When: expressionPointer(Expression{Op: "read", From: "binding", Name: "moved"}), Diagnostic: &ProtocolControlDiagnostic{
			Kind: "degrade", Source: expressionPointer(Expression{Op: "read", From: "binding", Name: "position"}), Reason: literalExpression("separate text from other content"),
		}}},
	}}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	native := &Native{Value: fixtureValue(t, Object{})}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Native: native, Content: []Node{
		{Kind: MessageNode, ID: StringValue("first"), Native: native, Children: []Node{
			{Kind: ReasoningNode, Payload: StringValue("thought"), Resources: []Resource{{Kind: "signature", ID: StringValue("signature")}}},
			{Kind: TextNode, Payload: StringValue("answer"), Native: native},
		}},
		{Kind: MessageNode, ID: StringValue("second"), Native: native, Children: []Node{{Kind: TextNode, Payload: StringValue("untouched")}}},
	}}
	before := fixtureValue(t, response)
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	sink := &DiagnosticSink{}
	result, err := conversion.Response(t.Context(), response, route, sink)
	if err != nil {
		t.Fatal(err)
	}
	first, second := result.Content[0], result.Content[1]
	if result.Native != nil || first.Native != nil || !first.ID.IsString("first") || !first.Children[0].Payload.IsString("answer") || first.Children[0].Native == nil || !first.Children[1].Resources[0].ID.IsString("signature") {
		t.Fatal("partition changed node owners", result)
	}
	if !second.ID.IsString("second") || second.Native == nil || !second.Children[0].Payload.IsString("untouched") || fixtureValue(t, response) != before {
		t.Fatal("partition crossed messages or changed its input", result)
	}
	issues := sink.Issues()
	if len(issues) != 1 || issues[0].Path != "/content/0/children/1" || issues[0].Code != ConversionDegraded {
		t.Fatal("partition lost the actual child location", issues)
	}
	sink = &DiagnosticSink{}
	if _, err := conversion.Response(t.Context(), result, route, sink); err != nil || len(sink.Issues()) != 0 {
		t.Fatal("already ordered content changed again", err, sink.Issues())
	}
}
