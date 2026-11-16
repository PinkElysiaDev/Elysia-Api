package server

import (
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func presetPackage(t testing.TB, id string) protocol.ProtocolPackage {
	t.Helper()
	packages, err := builtin.Packages()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range packages {
		var document protocol.ProtocolPackage
		if err := value.Decode(&document); err != nil {
			t.Fatal(err)
		}
		if document.Spec.ID == id {
			return document
		}
	}
	t.Fatalf("missing preset %s", id)
	return protocol.ProtocolPackage{}
}

func compileFixturePackage(t testing.TB, document protocol.ProtocolPackage) *protocol.Compiled {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustEncodedProtocolValue(t, document)
	compiled, issues := compiler.Compile(raw.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func compiledPackage(compiled *protocol.Compiled) protocol.ProtocolPackage {
	document, _ := compiled.Package()
	return document
}

func selectProtocolSamples(t testing.TB, compiled *protocol.Compiled, ids ...string) []protocol.Sample {
	t.Helper()
	samples := map[string]protocol.Sample{}
	for _, sample := range compiledPackage(compiled).Mapping.Samples {
		samples[sample.ID] = sample
	}
	selected := make([]protocol.Sample, 0, len(ids))
	for _, id := range ids {
		sample, exists := samples[id]
		if !exists {
			t.Fatal("missing protocol fixture", compiled.Identity().DefinitionID, id)
		}
		selected = append(selected, sample)
	}
	return selected
}

func checkGatewayWireStream(t *testing.T, target *protocol.Compiled, body string) {
	t.Helper()
	validator, err := target.NewWireStreamValidation(protocol.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Release()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		value, err := protocol.ParseValue([]byte(strings.TrimPrefix(line, "data: ")))
		if err == nil {
			err = validator.Consume(t.Context(), value)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := validator.Finish(); err != nil {
		t.Fatal(err)
	}
}

func addFixtureResponseEnvelope(t *testing.T, document *protocol.ProtocolPackage, modelField, model string) {
	t.Helper()
	for _, direction := range []protocol.Direction{protocol.DecodeResponse, protocol.EncodeResponse} {
		mapping := document.Mapping.Directions[direction]
		if direction == protocol.DecodeResponse {
			mapping.Program.Fields["model"] = protocol.Expression{Op: "read", Path: "/" + modelField}
			mapping.Program.Fields["attributes"] = protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"finishReason": {Op: "read", Path: "/finish"}}}
		} else {
			mapping.Program.Fields[modelField] = protocol.Expression{Op: "read", Path: "/model"}
			mapping.Program.Fields["finish"] = protocol.Expression{Op: "read", Path: "/attributes/finishReason"}
		}
		document.Mapping.Directions[direction] = mapping
	}
	for index, sample := range document.Mapping.Samples {
		if sample.Direction != protocol.DecodeResponse && sample.Direction != protocol.EncodeResponse {
			continue
		}
		wire, core := sample.Input, sample.Expected
		if sample.Direction == protocol.EncodeResponse {
			wire, core = core, wire
		}
		wireFields, _ := wire.ReadObject()
		coreFields, _ := core.ReadObject()
		wireFields[modelField] = protocol.StringValue(model)
		wireFields["finish"] = protocol.StringValue("stop")
		coreFields["model"] = protocol.StringValue(model)
		coreFields["attributes"] = mustProtocolValue(t, `{"finishReason":"stop"}`)
		wire = mustEncodedProtocolValue(t, wireFields)
		core = mustEncodedProtocolValue(t, coreFields)
		sample.Input, sample.Expected = wire, core
		if sample.Direction == protocol.EncodeResponse {
			sample.Input, sample.Expected = core, wire
		}
		document.Mapping.Samples[index] = sample
	}
}
