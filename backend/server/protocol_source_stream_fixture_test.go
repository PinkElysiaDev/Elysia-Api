package server

import (
	"fmt"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func chatStartupFrames(t *testing.T, sample protocol.Sample, firstTime int) []protocol.Value {
	t.Helper()
	var frames []protocol.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	canonicalTime := 1
	if firstTime == 81 {
		firstTime, canonicalTime = 1, 81
		for index, frame := range frames {
			value, _ := protocol.EncodeValue(canonicalTime)
			updated, err := protocol.ApplyMutations(frame, []protocol.Mutation{{Op: protocol.ReplaceValue, Path: "/created", Value: value}}, protocol.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			frames[index] = updated
		}
	}
	first, err := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"synthetic-prelude","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"usage":null,"system_fingerprint":null}`, firstTime)))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"r","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"service_tier":"auto"}`, canonicalTime)))
	return append([]protocol.Value{first, second}, frames...)
}

// The wire keeps a sequence that the mapping deliberately omits, so source
// validation must inspect the provider frame independently of semantic replay.
func sourceStreamPackage(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	read := func(path string) protocol.Expression {
		return protocol.Expression{Op: "read", From: "input", Path: path}
	}
	literal := func(value any) protocol.Expression {
		return protocol.Expression{Op: "literal", Value: mustEncodedProtocolValue(t, value)}
	}
	optionalEmit := func(name string) []protocol.ProtocolSemanticEffect {
		source := read("/" + name)
		return []protocol.ProtocolSemanticEffect{{ID: name, Kind: "emit", Path: "/" + name, Target: "/" + name, When: &protocol.Expression{Op: "exists", Source: &source}}}
	}
	decode := map[string]protocol.Expression{"schemaVersion": literal(protocol.ProtocolCoreVersion)}
	encode := map[string]protocol.Expression{}
	for _, name := range []string{"type", "responseId", "itemId", "item", "delta", "usage", "error"} {
		decode[name], encode[name] = read("/"+name), read("/"+name)
	}
	directions := map[protocol.Direction]protocol.ProtocolDirectionMapping{
		protocol.EncodeRequest:  {Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"model": read("/model")}}, Effects: optionalEmit("model")},
		protocol.DecodeResponse: {Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"schemaVersion": literal(protocol.ProtocolCoreVersion), "error": read("/error")}}, Effects: optionalEmit("error")},
		protocol.EncodeResponse: {Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"error": read("/error")}}, Effects: optionalEmit("error")},
		protocol.DecodeEvent:    {Program: protocol.Expression{Op: "object", Fields: decode}, Effects: optionalEmit("responseId")},
		protocol.EncodeEvent:    {Program: protocol.Expression{Op: "object", Fields: encode}, Effects: optionalEmit("responseId")},
	}
	contracts := map[protocol.Direction]protocol.WireContractSpec{}
	for direction := range directions {
		contracts[direction] = protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType, AllowUnknown: true}}
	}
	return protocol.ProtocolPackage{
		SchemaVersion: protocol.ProtocolPackageSchemaVersion,
		Spec: protocol.ProtocolSpec{ID: "source-stream-fixture", Version: "1", Family: "source-stream-fixture", WireVersion: "1",
			Operations: map[string]protocol.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON,
				Request: protocol.EncodeRequest, Response: protocol.DecodeResponse, Auth: protocol.Credential{Location: "none"}}},
			WireContracts: contracts,
			Stream: &protocol.ProtocolStreamSpec{Discriminator: "/type", ResponseID: "/responseId", ItemID: "/itemId", Sequence: "/sequence",
				Events: map[string]protocol.ProtocolStreamEventSpec{
					"response.started": {Action: "response_start"}, "response.finished": {Action: "response_finish"},
					"item.started": {Action: "item_start"}, "item.delta": {Action: "text_delta", Text: "/delta"}, "item.finished": {Action: "item_finish"},
					"usage.updated":    {Action: "usage", Usage: &protocol.ProtocolStreamUsageSpec{Input: "/usage/input/count", Output: "/usage/output/count", Total: "/usage/total/count"}},
					"operation.failed": {Action: "failure", Error: "/error"},
				}},
		},
		Mapping: protocol.ProtocolMapping{CoreVersion: protocol.ProtocolCoreVersion, DSLVersion: protocol.ProtocolDSLVersion,
			Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true}, Directions: directions},
	}
}

func compileSourceStreamPackage(t *testing.T, document protocol.ProtocolPackage) *protocol.Compiled {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompilePackage(mustEncodedProtocolValue(t, document).Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}
