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
		protocol.EncodeRequest:  {Document: "generation-request", Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"model": read("/model")}}, Effects: optionalEmit("model")},
		protocol.DecodeResponse: {Document: "provider-response", Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"schemaVersion": literal(protocol.ProtocolCoreVersion), "error": read("/error")}}, Effects: optionalEmit("error")},
		protocol.EncodeResponse: {Document: "client-response", Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"error": read("/error")}}, Effects: optionalEmit("error")},
		protocol.DecodeEvent:    {Document: "provider-event", Program: protocol.Expression{Op: "object", Fields: decode}, Effects: optionalEmit("responseId")},
		protocol.EncodeEvent:    {Document: "client-event", Program: protocol.Expression{Op: "object", Fields: encode}, Effects: optionalEmit("responseId")},
	}
	counter := protocol.ValueSchema{Type: protocol.ObjectType, AllowUnknown: true, Properties: map[string]protocol.ValueSchema{
		"count": {Type: protocol.IntegerType},
	}}
	stream := &protocol.ProtocolStreamSpec{Discriminator: "/type", ResponseID: "/responseId", Sequence: "/sequence",
		Namespaces: map[string]protocol.ProtocolStreamNamespaceSpec{"item": {KeyType: protocol.StringType}},
		Entities:   map[string]protocol.ProtocolStreamEntitySpec{"text": {Namespace: "item", Payload: "text"}},
		Lifecycle:  protocol.ProtocolStreamLifecycleSpec{EOF: "require_terminal", OpenEntities: "reject"},
		Events: map[string]protocol.ProtocolStreamEventSpec{
			"response.started": {{Action: "stream_start"}}, "response.finished": {{Action: "stream_finish"}},
			"item.started":  {{Action: "entity_start", Entity: "text", Key: &protocol.ProtocolStreamValueSpec{Path: "/itemId"}}},
			"item.delta":    {{Action: "entity_append", Entity: "text", Key: &protocol.ProtocolStreamValueSpec{Path: "/itemId"}, Payload: &protocol.ProtocolStreamValueSpec{Path: "/delta"}}},
			"item.finished": {{Action: "entity_finish", Entity: "text", Key: &protocol.ProtocolStreamValueSpec{Path: "/itemId"}}},
			"usage.updated": {{Action: "assert", AfterTerminal: true, Assertions: []protocol.WireAssertion{{Path: "/usage", Code: protocol.UpstreamContractViolation, Reason: "usage counters require integer count fields", Schema: &protocol.ValueSchema{Type: protocol.ObjectType, AllowUnknown: true, Properties: map[string]protocol.ValueSchema{
				"input": counter, "output": counter, "total": counter,
			}}}}}, {Action: "observe", AfterTerminal: true}},
			"operation.failed": {{Action: "stream_fail", Error: "/error"}},
		}}
	documents := map[string]protocol.WireContractSpec{}
	for direction, mapping := range directions {
		document := protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType, AllowUnknown: true}}
		if direction == protocol.DecodeEvent || direction == protocol.EncodeEvent {
			document.Stream = stream
		}
		documents[mapping.Document] = document
	}
	return protocol.ProtocolPackage{
		SchemaVersion: protocol.ProtocolPackageSchemaVersion,
		Spec: protocol.ProtocolSpec{ID: "source-stream-fixture", Version: "1", Family: "source-stream-fixture", WireVersion: "1",
			Operations: map[string]protocol.ProtocolOperationSpec{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON,
				Request: "generation-request", Response: "provider-response", Auth: protocol.Credential{Location: "none"}}},
			Documents: documents,
		},
		Mapping: protocol.ProtocolMapping{CoreVersion: protocol.ProtocolCoreVersion, DSLVersion: protocol.ProtocolDSLVersion,
			Operations:   map[string]protocol.ProtocolOperationMapping{"generate": {Request: protocol.EncodeRequest, Response: protocol.DecodeResponse}},
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

func outputAwareStreamPackage(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	document := sourceStreamPackage(t)
	document.Mapping.Native.Preserve = true
	document.Mapping.Directions[protocol.DecodeEvent].Program.Fields["sequence"] = protocol.Expression{Op: "read", From: "input", Path: "/sequence"}
	mapping := document.Mapping.Directions[protocol.EncodeEvent]
	for _, name := range []string{"responseId", "sequence"} {
		input := protocol.Expression{Op: "read", From: "input", Path: "/" + name}
		previous := protocol.Expression{Op: "read", From: "runtime", Path: "/stream/" + name}
		if name == "sequence" {
			previous = protocol.Expression{Op: "integer", Operation: "add", Items: []protocol.Expression{previous, {Op: "literal", Value: mustProtocolValue(t, "1")}}}
		}
		mapping.Program.Fields[name] = protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &input}, Then: &input, Otherwise: &previous}
	}
	mapping.Program.Fields["was_started"] = protocol.Expression{Op: "if", When: &protocol.Expression{Op: "equal", Items: []protocol.Expression{
		{Op: "read", From: "input", Path: "/type"}, {Op: "literal", Value: protocol.StringValue(string(protocol.OperationFailed))},
	}}, Then: &protocol.Expression{Op: "read", From: "runtime", Path: "/stream/started"}}
	mapping.Effects = []protocol.ProtocolSemanticEffect{{ID: "type", Kind: "emit", Path: "/type", Target: "/type"}}
	document.Mapping.Directions[protocol.EncodeEvent] = mapping
	return document
}
