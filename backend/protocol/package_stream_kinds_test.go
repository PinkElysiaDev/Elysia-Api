package protocol

import (
	"strings"
	"testing"
)

func explicitToolStreamPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := toolStreamPackage(t)
	stream := document.Spec.Documents["provider-event"].Stream
	delete(stream.Entities, "text")
	for _, kind := range []EventType{ItemStarted, ItemDelta, ItemFinished} {
		delete(stream.Events, string(kind))
	}
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		mapping := document.Mapping.Directions[direction]
		mapping.Capabilities = CapabilitySet{FunctionToolsCapability: true, UsageCapability: true}
		document.Mapping.Directions[direction] = mapping
	}
	return document
}

func TestPackageStreamExplicitEntities(t *testing.T) {
	document := explicitToolStreamPackage(t)
	if !document.Mapping.Capabilities[TextCapability] {
		t.Fatal("stream declaration changed unrelated JSON capabilities")
	}
	compilePackageForTest(t, document)
	// Mapping capabilities do not invent wire entities or their lifecycle.
	document.Mapping.Directions[EncodeEvent].Capabilities[TextCapability] = true
	compilePackageForTest(t, document)
	for _, fixture := range []struct {
		name, path string
		edit       func(*ProtocolPackage)
	}{
		{"undeclared-entity", "/events", func(p *ProtocolPackage) { delete(p.Spec.Documents["provider-event"].Stream.Entities, "function_call") }},
		{"undeclared-namespace", "/entities/function_call", func(p *ProtocolPackage) { delete(p.Spec.Documents["provider-event"].Stream.Namespaces, "item") }},
		{"missing-start", "/entities/function_call", func(p *ProtocolPackage) {
			delete(p.Spec.Documents["provider-event"].Stream.Events, string(packageToolStarted))
		}},
		{"core-kind-is-not-wire-payload", "/payload", func(p *ProtocolPackage) {
			stream := p.Spec.Documents["provider-event"].Stream
			entity := stream.Entities["function_call"]
			entity.Payload = "tool_call"
			stream.Entities["function_call"] = entity
		}},
		{"schema-requires-json", "/payloadSchema", func(p *ProtocolPackage) {
			stream := p.Spec.Documents["provider-event"].Stream
			entity := stream.Entities["function_call"]
			entity.Payload = "text"
			stream.Entities["function_call"] = entity
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := explicitToolStreamPackage(t)
			fixture.edit(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), fixture.path) {
				t.Fatal("invalid external entity declaration accepted", err)
			}
		})
	}
}
