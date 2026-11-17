package protocol

import (
	"reflect"
	"testing"
)

func TestLazyWalksKeepExactDiagnosticPaths(t *testing.T) {
	capabilities := CapabilitySet{}
	for _, capability := range CapabilityCatalog() {
		capabilities[capability] = true
	}
	target := Target{Direction: EncodeRequest, Capabilities: capabilities}
	for _, test := range []struct {
		name, path string
		request    *Request
		context    bool
	}{
		{"nested-content", "/content/0/children/0/children/0/kind", &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: NodeKind("unknown")}}}}}}}, false},
		{"reasoning-content", "/content/0/reasoningContent/0", &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: Value{raw: `42`}}}}}}, false},
		{"tool-name", "/tools/1/name", &Request{SchemaVersion: ProtocolCoreVersion, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("valid")}, {Kind: FunctionTool, Name: Value{raw: `null`}}}}, false},
		{"tool-cache", "/tools/1/cache/0/value", &Request{SchemaVersion: ProtocolCoreVersion, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("first")}, {Kind: FunctionTool, Name: StringValue("second"), Cache: []CacheIntent{{Kind: "key", Value: Value{raw: `42`}}}}}}, true},
		{"reasoning-cache", "/content/0/reasoningContent/0/cache/0/ttl", &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: ReasoningNode, ReasoningForm: StructuredReasoning, ReasoningContent: []Node{{Kind: TextNode, Payload: StringValue("visible"), Cache: []CacheIntent{{Kind: "breakpoint", Value: Value{raw: `null`}, TTL: StringValue("5m")}}}}}}}, true},
		{"nested-tool-error", "/content/0/children/0/reasoningContent/0/status/isError", &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: MessageNode, Children: []Node{{Kind: ReasoningNode, ReasoningContent: []Node{{Kind: ToolResultNode, Status: Value{raw: `{"isError":null}`}}}}}}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.context {
				assertPackageRuntimeIssue(t, validateRequestContext(test.request), InvalidInput, test.path)
				return
			}
			issues := CheckRequest(test.request, target, DefaultLimits())
			if len(issues) != 1 || issues[0].Code != InvalidInput || issues[0].Path != test.path {
				t.Fatalf("path/order changed: %#v", issues)
			}
		})
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: TextNode, Children: []Node{{Kind: TextNode}}, ReasoningContent: []Node{{Kind: TextNode}}}, {Kind: TextNode}}}
	var got []string
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}}
	if err := visitControlNodesPath(state, request, func(_ *Node, path diagnosticPath) error { got = append(got, path.String()); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"/content/0", "/content/0/children/0", "/content/0/reasoningContent/0", "/content/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("borrowed sibling paths changed", got, want)
	}
}

func TestLazyPathsDoNotAllocatePerVisitedNode(t *testing.T) {
	request := &Request{Content: []Node{{Kind: TextNode}}}
	visit := func(*Node, diagnosticPath) error { return nil }
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}}
	one := testing.AllocsPerRun(50, func() { state.budget.steps = 0; _ = visitControlNodesPath(state, request, visit) })
	request.Content = make([]Node, 128)
	many := testing.AllocsPerRun(50, func() { state.budget.steps = 0; _ = visitControlNodesPath(state, request, visit) })
	if many > one {
		t.Fatalf("paths allocated per node: %v / %v", one, many)
	}
	if allocs := testing.AllocsPerRun(50, func() { _ = validateToolResultErrors(request.Content, "/content") }); allocs != 0 {
		t.Fatalf("successful tool validation allocated paths: %v", allocs)
	}
}

func TestCoreControlContextIsReadLazily(t *testing.T) {
	resources := &controlEvaluationResources{model: "m", transport: HTTPJSON, budget: evaluationBudget{limits: DefaultLimits()}}
	state := evaluation{ctx: t.Context(), control: resources}
	compiler := newExpressionCompiler(DefaultLimits(), nil)
	literal, err := compiler.compile(literalExpression(false), "/test", expressionScope{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := compileOutputPlan(literal)
	if !ok {
		t.Fatal("literal plan did not compile")
	}
	if _, err = plan.evaluate(state); err != nil {
		t.Fatal(err)
	}
	if resources.contextReady || !resources.context.IsZero() || resources.objects != nil || resources.bindings != nil {
		t.Fatal("unused context/bindings were initialized")
	}
	value, err := resources.readContext([]string{"model"})
	if err != nil || !value.IsString("m") || !resources.contextReady {
		t.Fatal("actual context read did not initialize its value", value, err)
	}
}
