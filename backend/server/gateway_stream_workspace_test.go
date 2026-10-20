package server

import (
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayStreamWorkspaceReleasesFailedFrame(t *testing.T) {
	capabilities := protocol.CapabilitySet{}
	for _, capability := range protocol.CapabilityCatalog() {
		capabilities[capability] = true
	}
	for _, capacity := range []int{2, 64} {
		replay, err := protocol.NewEventReplay(protocol.Target{Direction: protocol.EncodeEvent, Capabilities: capabilities}, protocol.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		backing := make([]protocol.Event, capacity)
		stream := &gatewayStream{replay: replay, eventState: protocol.NewConversionEventState(nil, protocol.ConversionContext{}), acceptedScratch: backing[:0]}
		first := protocol.Event{SchemaVersion: 1, Type: protocol.ItemStarted, ItemID: protocol.StringValue("i"), Item: &protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue("payload must be released")}}
		frame := &protocol.EventFrame{Events: []protocol.Event{first, first}}
		if err := stream.consumeSourceFrame(frame, nil); err == nil {
			t.Fatal("duplicate start accepted")
		}
		for _, slot := range backing {
			if !reflect.DeepEqual(slot, protocol.Event{}) {
				t.Fatal("failed frame retained in workspace")
			}
		}
		if capacity > 16 && stream.acceptedScratch != nil {
			t.Fatal("oversized workspace retained")
		}
		if capacity <= 16 && cap(stream.acceptedScratch) != capacity {
			t.Fatal("small workspace was not reused")
		}
	}
}
