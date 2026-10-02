package protocol

import (
	"errors"
	"testing"
)

func TestRouteChecksModelCapabilitiesWithoutEditingContent(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("model", "model", "history", "tools", "answer"))
	binding := Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: CapabilitySet{TextCapability: true}, Transports: []Transport{HTTPJSON}}
	request := &Request{SchemaVersion: 1, Content: []Node{{Kind: TextNode, Payload: StringValue("hello")}}, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("lookup")}}}
	issues := CheckRoute(request, compiled, binding, Scope{}, HTTPJSON)
	if len(issues) == 0 || issues[0].Code != UnsupportedCapability || len(request.Tools) != 1 {
		t.Fatalf("incapable model changed/accepted request: %+v %+v", issues, request)
	}
	binding.Capabilities[FunctionToolsCapability] = true
	if issues := CheckRoute(request, compiled, binding, Scope{}, HTTPJSON); len(issues) != 0 {
		t.Fatalf("compatible model rejected: %+v", issues)
	}
	binding.RevisionHash = "stale"
	if issues := CheckBinding(binding, compiled); len(issues) == 0 || issues[0].Code != VerificationMismatch {
		t.Fatalf("stale binding accepted: %+v", issues)
	}
}

func TestRuntimeScopeResolverPreservesAuthorizationCause(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("scoped", "model", "history", "tools", "answer"))
	denied := errors.New("authorization denied")
	_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"group","history":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}]}]}`), EvaluationContext{ResolveRequestScope: func(request *Request) (Scope, error) { return Scope{}, denied }})
	if !errors.Is(err, denied) {
		t.Fatalf("authorization cause was lost: %v", err)
	}
}

func TestRuntimeStampsNestedEventScopesAndIdentity(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("scoped", "model", "history", "tools", "answer"))
	scope := Scope{Provider: "provider", Account: "opaque-account", Model: "m"}
	native := func() *Native {
		return &Native{Source: Provenance{Protocol: Identity{Family: "spoofed"}}, Value: StringValue("raw")}
	}
	resource := func() Resource { return Resource{Kind: "file", ID: StringValue("f")} }
	event := Event{Item: &Node{Kind: DocumentNode, Resources: []Resource{resource()}, Native: native(), Children: []Node{{Kind: TextNode, Native: native()}}}, Response: &Response{Native: native(), Content: []Node{{Kind: DocumentNode, Resources: []Resource{resource()}, Native: native()}}}, Media: &Media{Reference: resource()}}
	if err := compiled.stampEventProvenance(&event, DecodeEvent, scope); err != nil {
		t.Fatal(err)
	}
	for _, got := range []Scope{event.Item.Resources[0].Scope, event.Response.Content[0].Resources[0].Scope, event.Media.Reference.Scope} {
		if got != scope {
			t.Fatalf("unstamped reference: %+v", got)
		}
	}
	for _, got := range []*Native{event.Item.Native, event.Item.Children[0].Native, event.Response.Native, event.Response.Content[0].Native} {
		if got.Source.Protocol != compiled.Identity() || got.Source.Scope != scope || got.Source.Direction != DecodeEvent {
			t.Fatalf("spoofed provenance survived: %+v", got)
		}
	}
}

func TestCompiledOperationsReturnIndependentMetadata(t *testing.T) {
	definition := testDefinition("immutable", "model", "history", "tools", "answer")
	operation := definition.Operations["generate"]
	operation.Headers = map[string]string{"X-Mode": "original"}
	operation.Framing = &Framing{Done: []string{"[DONE]"}}
	definition.Operations["generate"] = operation
	compiled := compileTestDefinition(t, definition)
	copy := compiled.Operations()
	copy["generate"].Headers["X-Mode"] = "changed"
	copy["generate"].Framing.Done[0] = "changed"
	if compiled.Operations()["generate"].Headers["X-Mode"] != "original" || compiled.Operations()["generate"].Framing.Done[0] != "[DONE]" {
		t.Fatal("caller changed compiled transport metadata")
	}
}
