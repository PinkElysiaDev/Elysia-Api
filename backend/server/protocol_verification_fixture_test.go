package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
)

var liveProtocolIDs = []string{"openai-chat-completions", "openai-responses", "anthropic-messages", "google-generate-content"}

func TestVerificationEnvelopeTextCombinations(t *testing.T) {
	custom := compileFixtureDefinition(t, verificationEnvelopeDefinition(t))
	if report := protocol.Verify(t.Context(), custom); !report.Passed {
		t.Fatal(report.Issues)
	}
	for _, id := range liveProtocolIDs {
		t.Run(id, func(t *testing.T) {
			target := compileFixtureDefinition(t, presetDefinition(t, id))
			report := protocol.VerifyBindingCombination(t.Context(), custom, target, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
			if !report.Passed {
				t.Fatal(report.Issues)
			}
		})
	}
}

func verificationEnvelopeDefinition(t *testing.T) protocol.Definition {
	definition := agentEnvelopeDefinition(t)
	definition.Agent = nil
	textRequest := mustProtocolValue(t, `{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"parameters":{"max_output_tokens":128}}`)
	textWire := mustEncodedProtocolValue(t, protocol.Object{"payload": textRequest})
	for index := range definition.Samples {
		sample := &definition.Samples[index]
		if sample.Direction != protocol.DecodeRequest && sample.Direction != protocol.EncodeRequest {
			continue
		}
		for _, side := range []*protocol.Value{&sample.Input, &sample.Expected} {
			fields, err := side.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			path := "/content/2/name"
			if _, isWire := fields["payload"]; isWire {
				path = "/payload" + path
			}
			parameterPath := "/parameters"
			if _, isWire := fields["payload"]; isWire {
				parameterPath = "/payload/parameters"
			}
			value, err := protocol.ApplyMutations(*side, []protocol.Mutation{{Op: protocol.SetValue, Path: path, Value: protocol.StringValue("lookup")}, {Op: protocol.SetValue, Path: parameterPath, Value: mustProtocolValue(t, `{"max_output_tokens":128}`)}}, protocol.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			*side = value
		}
	}
	definition.Capabilities[protocol.UsageCapability] = true
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "text.request.decode", Context: protocol.Object{"model": protocol.StringValue("m")}, Direction: protocol.DecodeRequest, Input: textWire, Expected: textRequest, Capabilities: []protocol.Capability{protocol.TextCapability}},
		protocol.Sample{ID: "text.request.encode", Context: protocol.Object{"model": protocol.StringValue("m")}, Direction: protocol.EncodeRequest, Input: textRequest, Expected: textWire, Capabilities: []protocol.Capability{protocol.TextCapability}})
	responseMapping := definition.Directions[protocol.EncodeResponse]
	responseFields := responseMapping.Transform.Fields["payload"]
	for _, name := range []string{"model", "attributes", "error"} {
		responseFields.Fields[name] = protocol.Expression{Op: "read", Path: "/" + name}
	}
	responseMapping.Transform.Fields["payload"] = responseFields
	definition.Directions[protocol.EncodeResponse] = responseMapping
	usageResponse := mustProtocolValue(t, `{"schemaVersion":1,"id":"r","content":[{"kind":"text","payload":"ok"}],"usage":{"input":{"count":100,"origin":"observed"},"output":{"count":5,"origin":"observed"},"cacheRead":{"count":70,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"}}}`)
	usageWire := mustEncodedProtocolValue(t, protocol.Object{"payload": usageResponse})
	definition.Samples = append(definition.Samples, protocol.Sample{ID: "usage.decode", Direction: protocol.DecodeResponse, Input: usageWire, Expected: usageResponse}, protocol.Sample{ID: "usage.encode", Direction: protocol.EncodeResponse, Input: usageResponse, Expected: usageWire})
	definition.ID, definition.Family = "verification-envelope", "verification-envelope"
	events := gatewayStreamDefinition(t, "text-alpha", protocol.SSE, "event")
	for _, direction := range []protocol.Direction{protocol.DecodeEvent, protocol.EncodeEvent} {
		definition.Directions[direction] = events.Directions[direction]
	}
	mapping := definition.Directions[protocol.EncodeEvent]
	fields := mapping.Transform.Fields["event"]
	for _, name := range []string{"response", "responseId", "index", "callId", "sequence"} {
		fields.Fields[name] = protocol.Expression{Op: "read", Path: "/" + name}
	}
	mapping.Transform.Fields["event"] = fields
	definition.Directions[protocol.EncodeEvent] = mapping
	toolEvents := mustProtocolValue(t, `[{"schemaVersion":1,"type":"item.started","itemId":"tool","item":{"kind":"tool_call","callId":"call","name":"lookup","input":{"kind":"json"}}},{"schemaVersion":1,"type":"item.delta","itemId":"tool","delta":"{\"n\":1}"},{"schemaVersion":1,"type":"item.finished","itemId":"tool","item":{"kind":"tool_call","callId":"call","name":"lookup","input":{"kind":"json","value":{"n":1}}}},{"schemaVersion":1,"type":"response.finished"}]`)
	var semanticEvents []protocol.Event
	if err := toolEvents.Decode(&semanticEvents); err != nil {
		t.Fatal(err)
	}
	var toolWires []protocol.Value
	for _, event := range semanticEvents {
		fields, err := mustEncodedProtocolValue(t, event).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		delete(fields, "source")
		delete(fields, "schemaVersion")
		toolWires = append(toolWires, mustEncodedProtocolValue(t, protocol.Object{"event": mustEncodedProtocolValue(t, fields)}))
	}
	wireEvents := mustEncodedProtocolValue(t, toolWires)
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "tools.events.decode", Direction: protocol.DecodeEvent, Input: wireEvents, Expected: toolEvents, Sequence: true, Capabilities: []protocol.Capability{protocol.FunctionToolsCapability}},
		protocol.Sample{ID: "tools.events.encode", Direction: protocol.EncodeEvent, Input: toolEvents, Expected: wireEvents, Sequence: true, Capabilities: []protocol.Capability{protocol.FunctionToolsCapability}})
	for _, sample := range events.Samples {
		if sample.Sequence {
			definition.Samples = append(definition.Samples, sample)
		}
	}
	operation := definition.Operations["generate"]
	operation.Transport = protocol.SSE
	definition.Operations["stream"] = operation
	return definition
}
