package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestProtocolSchemaLibraryHTTPAndCLI(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	c, rec := adminProtocolContext(http.MethodGet, "/api/admin/protocols/schema", "")
	s.adminProtocolSchemaV2(c)
	data := decodeAdminData(t, rec)
	definition := data["definitionSchema"].(map[string]any)
	if definition["$ref"] != "#/$defs/Definition" || definition["$id"] != nil {
		t.Fatalf("unexpected root: %v", definition)
	}
	if definition["$defs"].(map[string]any)["CapabilitySet"] == nil {
		t.Fatal("named map definition missing over HTTP")
	}
	for _, command := range []string{
		"elysia protocol schema --section definition --type Expression",
		"elysia protocol schema --section semantic --type CacheIntent",
		"elysia protocol schema --section binding --type CapabilitySet",
	} {
		result := s.runCLI(t.Context(), &opsTestContext{}, command)
		if !result.OK {
			t.Fatalf("schema CLI failed: %+v", result)
		}
		output := result.Data.(map[string]any)["output"].(string)
		if !strings.Contains(output, `"type":"object"`) {
			t.Fatalf("missing schema: %s", output)
		}
	}
}
