package protocol

import (
	"strings"
	"testing"
)

func explicitToolStreamPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := toolStreamPackage(t)
	stream := document.Spec.Documents["provider-event"].Stream
	stream.ItemKinds = []NodeKind{ToolCallNode}
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

func TestPackageStreamExplicitItemKinds(t *testing.T) {
	document := explicitToolStreamPackage(t)
	if !document.Mapping.Capabilities[TextCapability] {
		t.Fatal("stream slice removed the protocol's JSON text capability")
	}
	compilePackageForTest(t, document)
	for _, fixture := range []struct {
		name, path string
		edit       func(*ProtocolPackage)
	}{
		{"default-still-needs-text", "/stream/events", func(p *ProtocolPackage) { p.Spec.Documents["provider-event"].Stream.ItemKinds = nil }},
		{"declared-text-needs-actions", "/stream/events", func(p *ProtocolPackage) {
			stream := p.Spec.Documents["provider-event"].Stream
			stream.ItemKinds = append(stream.ItemKinds, TextNode)
		}},
		{"undeclared-text-action", "/action", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events[string(ItemDelta)] = ProtocolStreamEventSpec{{Action: "text_delta", Text: "/text"}}
		}},
		{"direction-cannot-claim-text", "/mapping/directions/encode_event/capabilities", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeEvent].Capabilities[TextCapability] = true
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := explicitToolStreamPackage(t)
			fixture.edit(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), fixture.path) {
				t.Fatal("item declarations, actions and direction capabilities disagreed", err)
			}
		})
	}
}
