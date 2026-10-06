package builtin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// BenchmarkRequestWorkloads compares identical successful semantic requests.
// Native cache policies with no target equivalent are covered by correctness
// tests, not turned into faster error-path benchmark samples.
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
				request := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}}
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
				body, err := source.EncodeRequest(b.Context(), request, p.EvaluationContext{})
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					semantic, err := source.DecodeRequest(b.Context(), body, p.EvaluationContext{})
					if err != nil {
						b.Fatal(err)
					}
					if _, err := target.EncodeRequest(b.Context(), semantic, p.EvaluationContext{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func benchmarkEnvelope(b *testing.B) *p.Compiled {
	fields := map[string]p.Expression{}
	for _, name := range []string{"schemaVersion", "model", "content", "tools", "toolChoice", "parameters", "cache", "resources"} {
		fields[name] = p.Expression{Op: "read", Path: "/" + name}
	}
	definition := p.Definition{SchemaVersion: 2, ID: "benchmark-envelope", Name: "benchmark-envelope", Version: "1", Family: "benchmark-envelope", WireVersion: "1", Capabilities: p.CapabilitySet{p.TextCapability: true, p.FunctionToolsCapability: true}, Directions: map[p.Direction]p.Mapping{
		p.DecodeRequest: {Transform: &p.Expression{Op: "read", Path: "/packet"}},
		p.EncodeRequest: {Transform: &p.Expression{Op: "object", Fields: map[string]p.Expression{"packet": {Op: "object", Fields: fields}}}},
	}, Operations: map[string]p.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: p.HTTPJSON, Auth: p.Credential{Location: "none"}}}}
	raw, err := json.Marshal(definition)
	if err != nil {
		b.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	compiled, issues := compiler.Compile(raw)
	if compiled == nil {
		b.Fatal(issues)
	}
	return compiled
}
