package relay

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolSnapshotOrdersCallsAndPreservesScopes(t *testing.T) {
	body := []byte(`{"model":"m","system":[{"type":"text","text":"prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"assistant","content":[{"type":"text","text":"before"},{"type":"tool_use","id":"c1","name":"lookup","input":{"n":1}},{"type":"text","text":"after"}]}]}`)
	source := protocol.Identity{Family: "anthropic", WireVersion: "v1", DefinitionID: "copy", Revision: "r1"}
	snapshot, err := DecodeProtocolSnapshot(body, FormatClaude, "m", source, protocol.Scope{Provider: "anthropic", Account: "account-a", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Content) != 2 || len(snapshot.Content[0].Children[0].Cache) != 1 {
		t.Fatalf("system cache boundary lost: %+v", snapshot.Content)
	}
	children := snapshot.Content[1].Children
	if len(children) != 3 || children[0].Kind != protocol.TextNode || children[1].Kind != protocol.ToolCallNode || children[2].Kind != protocol.TextNode {
		t.Fatalf("native order lost: %+v", children)
	}
	if children[1].CallID != protocol.StringValue("c1") || children[1].Native.Source.Scope.Account != "account-a" {
		t.Fatalf("call provenance lost: %+v", children[1])
	}
	for _, forbidden := range []string{"messages", "input_items", "instructions", "tools"} {
		if _, hasDuplicate := snapshot.Parameters[forbidden]; hasDuplicate {
			t.Fatalf("second source of truth: %s", forbidden)
		}
	}
}

func TestProtocolSnapshotHasOneResponsesHistory(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"custom_tool_call","call_id":"c1","name":"patch","input":"not JSON"},{"type":"custom_tool_call_output","call_id":"c1","output":"done"}],"tools":[{"type":"custom","name":"patch","format":{"type":"text"}}]}`)
	snapshot, err := DecodeProtocolSnapshot(body, FormatResponses, "m", protocol.Identity{Family: "responses", WireVersion: "v1"}, protocol.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Content) != 2 || snapshot.Content[0].Input.Kind != protocol.TextInput || snapshot.Content[0].Input.Value != protocol.StringValue("not JSON") {
		t.Fatalf("call changed: %+v", snapshot.Content)
	}
	if snapshot.Content[1].Kind != protocol.ToolResultNode || snapshot.Content[1].CallID != snapshot.Content[0].CallID {
		t.Fatal("tool result association lost")
	}
	if snapshot.Tools[0].Kind != protocol.FreeTextTool {
		t.Fatal("free text tool misclassified")
	}
	if string(snapshot.Native.Value.Bytes()) != string(body) {
		t.Fatal("original native body lost")
	}
}
