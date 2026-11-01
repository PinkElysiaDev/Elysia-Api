package protocol

import "testing"

func TestDeclaredContentPartitionKeepsNodeOwners(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "response-layout", Order: 230, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{
		Operation: "stable_partition", Path: "/content", When: Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression("text")}},
	}}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Native: &Native{Value: fixtureValue(t, Object{})}, Content: []Node{
		{Kind: ReasoningNode, Payload: StringValue("thinking"), Resources: []Resource{{Kind: "signature", ID: StringValue("signed")}}},
		{Kind: TextNode, Payload: StringValue("first"), Attributes: Object{"wire:custom": fixtureValue(t, Object{"n": Value{raw: "9007199254740993"}})}, Native: &Native{Value: fixtureValue(t, Object{})}},
		{Kind: ReasoningNode, Payload: StringValue("later")},
		{Kind: TextNode, Payload: StringValue("last"), Cache: []CacheIntent{{Kind: "breakpoint", Value: fixtureValue(t, Object{})}}},
	}}
	before := fixtureValue(t, response)
	result, err := conversion.Response(t.Context(), response, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Native != nil || len(result.Content) != 4 || !result.Content[0].Payload.IsString("first") || !result.Content[1].Payload.IsString("last") || !result.Content[2].Payload.IsString("thinking") || !result.Content[3].Payload.IsString("later") {
		t.Fatal("stable partition changed node order", result)
	}
	if result.Content[0].Native == nil || result.Content[0].Attributes["wire:custom"].IsZero() || len(result.Content[1].Cache) != 1 || len(result.Content[2].Resources) != 1 || !result.Content[2].Resources[0].ID.IsString("signed") {
		t.Fatal("moving a node changed its payload owners", result.Content)
	}
	if fixtureValue(t, response) != before {
		t.Fatal("partition mutated caller-owned nodes")
	}
}

func TestContentLayoutCompileBoundary(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		phase  ConversionPhase
		change func(*ProtocolContentLayout)
	}{
		{"event", ConversionEvent, func(*ProtocolContentLayout) {}},
		{"protected-path", ConversionRequest, func(layout *ProtocolContentLayout) { layout.Path = "/resources" }},
		{"duplicate-binding", ConversionRequest, func(layout *ProtocolContentLayout) { layout.PathBinding, layout.MovedBinding = "position", "position" }},
		{"protected-write", ConversionRequest, func(layout *ProtocolContentLayout) {
			layout.Steps = []ProtocolControlStep{{Set: map[string]Expression{"/native": {Op: "omit"}}}}
		}},
		{"combine-update", ConversionRequest, func(layout *ProtocolContentLayout) {
			layout.Operation = "combine_children"
			layout.Steps = []ProtocolControlStep{{Set: map[string]Expression{"/role": literalExpression("user")}}}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := eventProtocolPackage(t)
			layout := &ProtocolContentLayout{Operation: "stable_partition", Path: "/content", When: literalExpression(true)}
			fixture.change(layout)
			document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "layout", Order: 140, Enabled: true, Phase: fixture.phase, Steps: []ProtocolControlStep{{Layout: layout}}}}
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			if compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("invalid layout compiled", issues)
			}
		})
	}
}
