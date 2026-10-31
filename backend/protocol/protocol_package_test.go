package protocol

import (
	"strings"
	"testing"
)

func compileWireContractSpec(spec WireContractSpec, path string, limits Limits) (compiledWireContract, error) {
	return newExpressionCompiler(limits, nil).compileWireContractSpec(spec, path)
}

func compilePackageStreamContract(spec *ProtocolStreamSpec, path string, limits Limits) (*compiledPackageStreamContract, error) {
	return newExpressionCompiler(limits, nil).compilePackageStreamContract(spec, path)
}

func textProtocolPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	message := func(role, path string) Expression {
		return Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
			"kind": literalExpression("message"), "role": literalExpression(role),
			"children": {Op: "array", Items: []Expression{objectExpression(map[string]Expression{
				"kind": literalExpression("text"), "payload": readExpression("input", path),
			})}},
		})}}
	}
	request := ValueSchema{Type: ObjectType, Required: []string{"model", "prompt"}, Properties: map[string]ValueSchema{"model": {Type: StringType}, "prompt": {Type: StringType}}}
	response := ValueSchema{Type: ObjectType, Required: []string{"reply"}, Properties: map[string]ValueSchema{"reply": {Type: StringType}}}
	return ProtocolPackage{
		SchemaVersion: ProtocolPackageSchemaVersion,
		Spec: ProtocolSpec{ID: "declarative-text", Name: "Declarative text", Version: "1", Family: "declarative-text", WireVersion: "1",
			Operations: map[string]ProtocolOperationSpec{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: HTTPJSON, Request: "generation-request", Response: "provider-response", Auth: Credential{Location: "none"}}},
			Documents:  map[string]WireContractSpec{"request": {Schema: &request}, "generation-request": {Schema: &request}, "provider-response": {Schema: &response}, "client-response": {Schema: &response}},
		},
		Mapping: ProtocolMapping{CoreVersion: ProtocolCoreVersion, DSLVersion: ProtocolDSLVersion, Capabilities: CapabilitySet{TextCapability: true},
			Operations: map[string]ProtocolOperationMapping{"generate": {Request: EncodeRequest, Response: DecodeResponse}},
			Directions: map[Direction]ProtocolDirectionMapping{
				DecodeRequest:  {Document: "request", Program: objectExpression(map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "model": readExpression("input", "/model"), "content": message("user", "/prompt")}), Effects: []ProtocolSemanticEffect{{ID: "model", Kind: "emit", Path: "/model", Target: "/model"}, {ID: "prompt", Kind: "emit", Path: "/content/0/children/0/payload", Target: "/prompt"}}},
				EncodeRequest:  {Document: "generation-request", Program: objectExpression(map[string]Expression{"model": readExpression("input", "/model"), "prompt": readExpression("input", "/content/0/children/0/payload")}), Effects: []ProtocolSemanticEffect{{ID: "model", Kind: "emit", Path: "/model", Target: "/model"}, {ID: "prompt", Kind: "emit", Path: "/content/0/children/0/payload", Target: "/prompt"}}},
				DecodeResponse: {Document: "provider-response", Program: objectExpression(map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "status": literalExpression("completed"), "content": message("assistant", "/reply")}), Effects: []ProtocolSemanticEffect{{ID: "reply", Kind: "emit", Path: "/content/0/children/0/payload", Target: "/reply"}}},
				EncodeResponse: {Document: "client-response", Program: objectExpression(map[string]Expression{"reply": readExpression("input", "/content/0/children/0/payload")}), Effects: []ProtocolSemanticEffect{{ID: "reply", Kind: "emit", Path: "/content/0/children/0/payload", Target: "/reply"}}},
			},
		},
	}
}

func TestProtocolPackageDecodeContract(t *testing.T) {
	document := textProtocolPackage(t)
	encoded, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := DecodeProtocolPackage(encoded.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Spec.Identity() != document.Spec.Identity() || parsed.Mapping.CoreVersion != ProtocolCoreVersion {
		t.Fatal("package identity or core contract changed", parsed.Spec.Identity())
	}
}

func TestProtocolPackageRejectsInvalidContracts(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		change        func(*ProtocolPackage)
	}{
		{"v2-package", "schemaVersion", func(p *ProtocolPackage) { p.SchemaVersion = 2 }},
		{"legacy-core", "coreVersion", func(p *ProtocolPackage) { p.Mapping.CoreVersion = 1 }},
		{"unknown-dsl", "dslVersion", func(p *ProtocolPackage) { p.Mapping.DSLVersion++ }},
		{"missing-document", "wire document", func(p *ProtocolPackage) { delete(p.Spec.Documents, p.Mapping.Directions[DecodeRequest].Document) }},
		{"duplicate-effect", "unique effect", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[DecodeRequest]
			entry.Effects = append(entry.Effects, entry.Effects[0])
			p.Mapping.Directions[DecodeRequest] = entry
		}},
		{"decode-drop", "cannot default or drop", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Effects[0].Kind = "drop"
		}},
		{"nonboolean-condition", "boolean", func(p *ProtocolPackage) {
			condition := literalExpression("not a boolean")
			p.Mapping.Directions[DecodeRequest].Effects[0].When = &condition
		}},
		{"invalid-effect-pointer", "pointer", func(p *ProtocolPackage) { p.Mapping.Directions[DecodeRequest].Effects[0].Path = "model" }},
		{"unknown-program", "unsupported mapping operation", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[DecodeRequest]
			entry.Program = Expression{Op: "anthropic"}
			p.Mapping.Directions[DecodeRequest] = entry
		}},
		{"unused-expression", "unreachable expression", func(p *ProtocolPackage) {
			p.Mapping.Expressions = map[string]Expression{"unused": literalExpression(true)}
		}},
		{"task-missing-program", "declared programs", func(p *ProtocolPackage) {
			operation := p.Spec.Operations["generate"]
			operation.Kind = "submit"
			p.Spec.Operations["generate"] = operation
		}},
		{"raised-budget", "cannot exceed", func(p *ProtocolPackage) {
			limits := DefaultLimits()
			limits.BufferBytes++
			p.Mapping.Limits = &limits
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			tc.change(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
	encoded, err := EncodeValue(textProtocolPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{`"module":"anthropic",`, `"after":{"op":"read"},`} {
		raw := strings.Replace(encoded.JSONString(), `"program":`, extra+`"program":`, 1)
		if _, err := DecodeProtocolPackage([]byte(raw), DefaultLimits()); err == nil {
			t.Fatal("legacy executable mapping accepted", extra)
		}
	}
}
