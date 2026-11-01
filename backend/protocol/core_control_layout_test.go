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
		{"group-role", ConversionRequest, func(layout *ProtocolContentLayout) {
			layout.Operation = "group_runs"
		}},
		{"group-boundary", ConversionRequest, func(layout *ProtocolContentLayout) {
			layout.Operation, layout.Role = "group_runs", "assistant"
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

func TestDeclaredContentGroupingKeepsRunsAndOwners(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "group-history", Order: 165, Enabled: true, Phase: ConversionRequest, Steps: []ProtocolControlStep{{Layout: &ProtocolContentLayout{
		Operation: "group_runs", Path: "/content", Role: "assistant", PathBinding: "position",
		When:  Expression{Op: "not", Source: expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/role"), literalExpression("user")}})},
		Steps: []ProtocolControlStep{{Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Source: expressionPointer(Expression{Op: "read", From: "binding", Name: "position"}), Reason: literalExpression("root boundaries become one message")}}},
	}}}}}
	compiled := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{
		{Kind: ReasoningNode, Payload: StringValue("thinking"), Resources: []Resource{{Kind: "signature", ID: StringValue("signed")}}},
		{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: TextNode, Payload: StringValue("first"), Native: &Native{Value: fixtureValue(t, Object{})}}}},
		{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("barrier")}}},
		{Kind: ReasoningNode, Payload: StringValue("later")},
	}}
	before := fixtureValue(t, request)
	sink := &DiagnosticSink{}
	result, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 3 || len(result.Content[0].Children) != 2 || !result.Content[1].Role.IsString("user") || len(result.Content[2].Children) != 1 || !result.Content[2].Children[0].Payload.IsString("later") {
		t.Fatal("grouping crossed a barrier or changed run order", result.Content)
	}
	if len(result.Content[0].Children[0].Resources) != 1 || result.Content[0].Children[1].Native == nil || len(sink.Issues()) != 3 || fixtureValue(t, request) != before {
		t.Fatal("grouping changed ownership or hid boundary loss", result.Content, sink.Issues())
	}
	request.Content[1].Attributes = Object{"wire:custom": StringValue("retained")}
	if _, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil); err == nil {
		t.Fatal("grouping discarded container semantics")
	}
}
