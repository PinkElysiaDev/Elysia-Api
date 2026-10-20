package protocol

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate the reflection library's output with our runtime schema compiler.
func resolveCatalogSchema(t *testing.T, schema map[string]any) *validator.Schema {
	t.Helper()
	compiler := validator.NewCompiler()
	const location = "urn:elysia:catalog-test"
	if err := compiler.AddResource(location, schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func schemaInstance(t *testing.T, value any) any {
	t.Helper()
	encoded, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := sonic.ConfigStd.Unmarshal(encoded, &instance); err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestSchemaCatalogRootsAndStrictDefinition(t *testing.T) {
	t.Parallel()
	catalog := DescribeSchema()
	for name, schema := range map[string]map[string]any{
		"definition": catalog.Definition, "binding": catalog.Binding,
		"conversion": catalog.Conversion["policy"].(map[string]any),
	} {
		t.Run(name, func(t *testing.T) { resolveCatalogSchema(t, schema) })
	}
	for _, name := range []string{"request", "response", "event", "task", "models", "agent"} {
		t.Run(name, func(t *testing.T) {
			root := catalog.Semantic[name].(map[string]any)
			resolveCatalogSchema(t, map[string]any{"$ref": root["$ref"], "$defs": catalog.Semantic["$defs"], "$schema": catalog.Semantic["$schema"]})
		})
	}
	resolved := resolveCatalogSchema(t, catalog.Definition)
	instance := schemaInstance(t, testDefinition("schema", "model", "messages", "tools", "reply")).(map[string]any)
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("real recursive definition rejected: %v", err)
	}
	instance["surprise"] = true
	if err := resolved.Validate(instance); err == nil {
		t.Fatal("unknown root field accepted")
	}
	delete(instance, "surprise")
	delete(instance, "schemaVersion")
	if err := resolved.Validate(instance); err == nil {
		t.Fatal("missing required schemaVersion accepted")
	}
}

func TestSchemaValuePresenceAndRecursiveReferences(t *testing.T) {
	t.Parallel()
	catalog := DescribeSchema()
	resolveType := func(root map[string]any, name string) *validator.Schema {
		return resolveCatalogSchema(t, map[string]any{"$ref": "#/$defs/" + name, "$defs": root["$defs"]})
	}
	expression := resolveType(catalog.Definition, "Expression")
	for _, value := range []any{nil, false, 0, "", []any{nil, false}, map[string]any{"n": 9007199254740993123}} {
		if err := expression.Validate(map[string]any{"op": "literal", "value": schemaInstance(t, value)}); err != nil {
			t.Fatalf("arbitrary JSON restricted: %v", err)
		}
	}
	if err := expression.Validate(map[string]any{"op": "literal"}); err != nil {
		t.Fatalf("omitzero field became required: %v", err)
	}
	if err := expression.Validate(map[string]any{"op": "if", "then": map[string]any{}}); err == nil {
		t.Fatal("recursive required field not checked")
	}
	if err := expression.Validate(map[string]any{"op": "if", "then": map[string]any{"op": "literal", "typo": 1}}); err == nil {
		t.Fatal("unknown recursive field accepted")
	}
	capabilities := resolveType(catalog.Definition, "CapabilitySet")
	if err := capabilities.Validate(map[string]any{"text": false}); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.Validate(map[string]any{"text": "false"}); err == nil {
		t.Fatal("named map lost boolean constraint")
	}
	cache := resolveType(catalog.Semantic, "CacheIntent")
	for _, raw := range []string{`{"kind":"breakpoint","location":"node"}`, `{"kind":"breakpoint","location":"node","value":null,"ttl":null}`, `{"kind":"breakpoint","location":"node","value":false,"ttl":0}`} {
		var instance any
		if err := sonic.ConfigStd.Unmarshal([]byte(raw), &instance); err != nil {
			t.Fatal(err)
		}
		if err := cache.Validate(instance); err != nil {
			t.Fatalf("presence/null contract: %v", err)
		}
	}
	props := catalog.Semantic["$defs"].(map[string]any)["CacheIntent"].(map[string]any)["properties"].(map[string]any)
	if !strings.Contains(props["ttl"].(map[string]any)["description"].(string), "explicit null") {
		t.Fatal("TTL description lost")
	}
}

func TestSchemaCatalogFreshResults(t *testing.T) {
	t.Parallel()
	first := DescribeSchema()
	first.Definition["$defs"].(map[string]any)["Definition"].(map[string]any)["additionalProperties"] = true
	first.Semantic["$defs"].(map[string]any)["Request"] = nil
	second := DescribeSchema()
	if second.Definition["$defs"].(map[string]any)["Definition"].(map[string]any)["additionalProperties"] != false || second.Semantic["$defs"].(map[string]any)["Request"] == nil {
		t.Fatal("catalog mutation leaked into another request")
	}
}
