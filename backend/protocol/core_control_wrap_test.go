package protocol

import "testing"

func wrapperPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "wrapper", Order: 230, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{
		Operation: "wrap_nodes", Path: "/content", Role: "assistant", When: literalExpression(true),
		Wrapper: &ProtocolNodeWrapper{ID: readExpression("context", "/model"), Status: literalExpression("completed")},
	}}}}}
	return document
}

func TestDeclaredWrapperPreservesChildAndReadsBoundContext(t *testing.T) {
	compiled := compilePackageForTest(t, wrapperPackage(t))
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: TextNode, ID: StringValue("leaf"), Payload: StringValue("text"), Attributes: Object{"tag": StringValue("keep")}, Resources: []Resource{{Kind: "signature", ID: StringValue("signed")}}}}}
	before := fixtureValue(t, response)
	result, err := conversion.Response(t.Context(), response, ConversionContext{Target: compiled.identity, Model: "wrapper-id"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || !result.Content[0].ID.IsString("wrapper-id") || !result.Content[0].Status.IsString("completed") || len(result.Content[0].Children) != 1 || fixtureValue(t, result.Content[0].Children[0]) != fixtureValue(t, response.Content[0]) {
		t.Fatal("wrapper changed the selected node or ignored context", result.Content)
	}
	if fixtureValue(t, response) != before || result.Content[0].Native != nil || len(result.Content[0].Resources) != 0 {
		t.Fatal("wrapper mutated its input or gained child authority")
	}
}

func TestDeclaredWrapperCompileBoundary(t *testing.T) {
	for _, name := range []string{"wrong-operation", "missing-role", "number-id", "node-update", "merge-text"} {
		t.Run(name, func(t *testing.T) {
			document := wrapperPackage(t)
			layout := document.Mapping.ConversionDefaults[0].Steps[0].Layout
			switch name {
			case "wrong-operation":
				layout.Operation = "stable_partition"
			case "missing-role":
				layout.Role = ""
			case "number-id":
				layout.Wrapper.ID = literalExpression(4)
			case "node-update":
				layout.Steps = []ProtocolControlStep{{Set: map[string]Expression{"/payload": literalExpression("changed")}}}
			case "merge-text":
				layout.Operation, layout.Path, layout.Role = "merge_text", "/content/*/children", ""
				layout.Text = &ProtocolTextLayout{Measure: literalExpression(0)}
			}
			compiler, _ := NewCompiler(DefaultLimits(), nil)
			if compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("invalid wrapper compiled", issues)
			}
		})
	}
}
