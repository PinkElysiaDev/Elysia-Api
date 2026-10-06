package protocol

import "testing"

func TestCapabilityChecksDoNotDowngradeRequests(t *testing.T) {
	request := &Request{SchemaVersion: SemanticSchemaVersion, Content: []Node{{Kind: TextNode, Payload: StringValue("hi")}}}
	target := Target{Direction: EncodeRequest, Capabilities: CapabilitySet{TextCapability: true}}
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal(issues)
	}
	request.Tools = []Tool{{Kind: FunctionTool, Name: StringValue("lookup")}}
	issues := CheckRequest(request, target, DefaultLimits())
	if len(issues) != 1 || issues[0].Code != UnsupportedCapability || issues[0].Path != "/tools/0" || issues[0].Capability != FunctionToolsCapability {
		t.Fatal(issues)
	}
	if len(request.Tools) != 1 {
		t.Fatal("validation removed incompatible tools")
	}
}

func TestCallAndResultAssociation(t *testing.T) {
	call := Node{Kind: ToolCallNode, Name: StringValue("patch"), CallID: StringValue("c1"), Input: &ToolInput{Kind: TextInput, Value: StringValue("")}}
	result := Node{Kind: ToolResultNode, CallID: StringValue("c1"), Payload: parseTestValue(t, `null`)}
	target := Target{Direction: EncodeRequest, Capabilities: CapabilitySet{FreeTextToolsCapability: true}}
	request := &Request{SchemaVersion: SemanticSchemaVersion, Content: []Node{call, result}}
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, content := range [][]Node{{result, call}, {call, call}, {call, result, result}} {
		request.Content = content
		issues := CheckRequest(request, target, DefaultLimits())
		if len(issues) == 0 || issues[0].Code != InvalidAssociation {
			t.Fatalf("invalid association accepted: %+v", content)
		}
	}
	call.Input.Value = parseTestValue(t, `{}`)
	request.Content = []Node{call}
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 1 || issues[0].Code != InvalidInput {
		t.Fatal(issues)
	}
}

func TestResourceConstraintsCannotBeMappedAway(t *testing.T) {
	request := &Request{SchemaVersion: SemanticSchemaVersion, Content: []Node{}, Cache: []CacheIntent{{Kind: "resource", Resource: &Resource{Kind: "cache", ID: StringValue("cached/1"), Scope: Scope{Provider: "p", Account: "a", Model: "m"}}}}}
	target := Target{Direction: EncodeRequest, Scope: Scope{Provider: "p", Account: "b", Model: "m"}, Capabilities: CapabilitySet{CacheResourcesCapability: true}}
	issues := CheckRequest(request, target, DefaultLimits())
	if len(issues) != 1 || issues[0].Code != ResourceScopeMismatch {
		t.Fatal(issues)
	}
	target.Scope.Account = "a"
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestVerificationBinding(t *testing.T) {
	report := VerificationReport{DefinitionHash: "definition-a", CompilerVersion: "engine-a", SamplesHash: "samples-a", Kind: OfflineVerification, Passed: true}
	if !report.IsCurrent("definition-a", "engine-a", "samples-a") {
		t.Fatal("matching evidence rejected")
	}
	if report.IsCurrent("definition-b", "engine-a", "samples-a") || report.IsCurrent("definition-a", "engine-b", "samples-a") || report.IsCurrent("definition-a", "engine-a", "samples-b") {
		t.Fatal("stale evidence reused")
	}
}
