package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestLegacyImportExecutesCompiledMappingWithoutLegacyTemplateRuntime(t *testing.T) {
	raw := []byte(`{"id":"copy-any-id","request":{"shape":"responses","path":"/v1/custom","auth":{"mode":"bearer"},"body":{"deployment":{"field":"model"},"history":{"field":"input_items"},"actions":{"field":"tools"}}},"response":{"adapter":"responses"}}`)
	definition, issues := ImportLegacyProtocol(raw, protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: true})
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	compiler, err := NewProtocolCompiler(protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(encoded)
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	request := &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue("m"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hello")}}}}}
	body, err := compiled.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"deployment":"m"`, `"history":[`, `"actions":null`, "hello"} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("lost import behavior %s: %s", expected, body)
		}
	}
	if strings.Contains(string(body), `"tools"`) {
		t.Fatalf("ignored target path: %s", body)
	}
}

func TestLegacyImportBlocksUnprovenFeaturesAndRetainsOriginal(t *testing.T) {
	raw := []byte(`{"id":"needs-repair","request":{"path":"/v1/test","bodyTemplate":"{\"model\":\"{{maheshvara.model}}\"}"},"response":{"textPath":"answer","stream":{"mode":"cumulative"}},"aliases":{"usage":{"cached":["saved"]}}}`)
	definition, issues := ImportLegacyProtocol(raw, protocol.CapabilitySet{protocol.TextCapability: true})
	if definition == nil || len(issues) < 3 {
		t.Fatalf("silently accepted unsupported import: %+v", issues)
	}
	if string(definition.Extensions["legacyImport"].Bytes()) != string(raw) {
		t.Fatal("lost user configuration needed for repair")
	}
	for _, issue := range issues {
		if issue.Stage != "import" || issue.Path == "" || issue.Severity != protocol.SeverityError {
			t.Fatalf("unlocatable diagnostic: %+v", issue)
		}
	}
}
