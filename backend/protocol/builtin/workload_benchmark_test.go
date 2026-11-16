package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func BenchmarkRequestWorkloads(b *testing.B) {
	for _, path := range []string{"same", "anthropic", "responses", "declarative"} {
		for _, workload := range []string{"short", "prefix32k", "history256k", "tools32"} {
			b.Run(path+"/"+workload, func(b *testing.B) {
				source, target := testCompiled(b, Chat), testCompiled(b, Chat)
				if path == "anthropic" {
					target = testCompiled(b, Anthropic)
				}
				if path == "responses" {
					target = testCompiled(b, Responses)
				}
				if path == "declarative" {
					source = benchmarkEnvelope(b)
				}
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}}
				switch workload {
				case "prefix32k":
					request.Content[0].Children[0].Payload = p.StringValue(strings.Repeat("p", 32<<10))
				case "history256k":
					request.Content = nil
					for index := range 64 {
						role := "user"
						if index%2 == 1 {
							role = "assistant"
						}
						request.Content = append(request.Content, p.Node{Kind: p.MessageNode, Role: p.StringValue(role), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(strings.Repeat("h", 4<<10))}}})
					}
				case "tools32":
					schema, err := p.ParseValue([]byte(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`))
					if err != nil {
						b.Fatal(err)
					}
					for index := range 32 {
						request.Tools = append(request.Tools, p.Tool{Kind: p.FunctionTool, Name: p.StringValue(fmt.Sprintf("lookup_%d", index)), InputSchema: schema})
					}

				}
				conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
				if err != nil {
					b.Fatal(err)
				}
				route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m"}
				body, err := source.EncodeRequest(b.Context(), request, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					semantic, err := source.DecodeRequest(b.Context(), body, fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						b.Fatal(err)
					}
					semantic, err = conversion.Request(b.Context(), semantic, route, nil)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := target.EncodeRequest(b.Context(), semantic, fixtureOptions(p.EvaluationContext{})); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
func benchmarkEnvelope(b *testing.B) *p.Compiled {
	b.Helper()
	encodeFields, decodeFields := map[string]p.Expression{}, map[string]p.Expression{}
	for _, name := range []string{"schemaVersion", "model", "content", "tools", "toolChoice", "parameters", "cache", "resources"} {
		encodeFields[name] = p.Expression{Op: "read", Path: "/" + name}
		decodeFields[name] = p.Expression{Op: "read", Path: "/packet/" + name}
	}
	schema := p.ValueSchema{Type: p.ObjectType, Required: []string{"packet"}, Properties: map[string]p.ValueSchema{
		"packet": {Type: p.ObjectType, Required: []string{"schemaVersion", "model", "content"}, Properties: map[string]p.ValueSchema{
			"schemaVersion": {Type: p.NumberType}, "model": {Type: p.StringType}, "content": {Type: p.ArrayType}, "tools": {Type: p.ArrayType},
			"parameters": {Type: p.ObjectType}, "toolChoice": {Type: p.ObjectType}, "cache": {Type: p.ArrayType}, "resources": {Type: p.ArrayType},
		}},
	}}
	effects := []p.ProtocolSemanticEffect{{ID: "model", Kind: "emit", Path: "/model", Target: "/packet/model"}, {ID: "content", Kind: "emit", Path: "/content", Target: "/packet/content"}}
	document := p.ProtocolPackage{SchemaVersion: p.ProtocolPackageSchemaVersion,
		Spec: p.ProtocolSpec{ID: "benchmark-envelope", Name: "Benchmark envelope", Version: "1", Family: "benchmark-envelope", WireVersion: "1",
			Documents:  map[string]p.WireContractSpec{"packet": {Schema: &schema}},
			Operations: map[string]p.ProtocolOperationSpec{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: p.HTTPJSON, Request: "packet", Auth: p.Credential{Location: "none"}}}},
		Mapping: p.ProtocolMapping{CoreVersion: p.ProtocolCoreVersion, DSLVersion: p.ProtocolDSLVersion,
			Capabilities: p.CapabilitySet{p.TextCapability: true, p.FunctionToolsCapability: true},
			Operations:   map[string]p.ProtocolOperationMapping{"generate": {Request: p.EncodeRequest}},
			Directions: map[p.Direction]p.ProtocolDirectionMapping{
				p.DecodeRequest: {Document: "packet", Program: p.Expression{Op: "object", Fields: decodeFields}, Effects: effects},
				p.EncodeRequest: {Document: "packet", Program: p.Expression{Op: "object", Fields: map[string]p.Expression{"packet": {Op: "object", Fields: encodeFields}}}, Effects: effects},
			}},
	}
	compiled, issues := compilePackageDocument(b, document)
	if err := p.IssuesError(issues); err != nil {
		b.Fatal(err)
	}
	return compiled
}
