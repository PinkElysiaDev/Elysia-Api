package server

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayStreamExpandedFramePreservesSequenceConflict(t *testing.T) {
	document := sourceStreamPackage(t)
	document.Mapping.Directions[protocol.DecodeEvent].Program.Fields["sequence"] = protocol.Expression{Op: "read", From: "input", Path: "/sequence"}
	compiled := compileSourceStreamPackage(t, document)
	conversion, err := protocol.CompileConversion(protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "buffer-text", Rules: []protocol.ConversionRule{{ID: "wait-text", Enabled: true, Phase: protocol.ConversionEvent, Action: "buffer_node", Match: protocol.ConversionMatch{NodeKind: protocol.TextNode}}}})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := protocol.NewEventReplay(compiled.Target(protocol.EncodeEvent, protocol.Scope{}), protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Release()
	state := protocol.NewConversionEventState(conversion, protocol.ConversionContext{})
	defer state.Release()
	// If the reused sequence is silently deduplicated, reaching delivery must
	// fail this test for the right reason rather than panic on an unused writer.
	unexpectedDelivery := errors.New("conflicting projected events reached delivery")
	stream := &gatewayStream{ctx: t.Context(), replay: replay, eventState: state, candidate: gatewayCandidate{conversion: conversion}, plan: &gatewayResponsePlan{ingress: compiled}, deliveryFailure: unexpectedDelivery}
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	defer options.State.Release()
	header, err := compiled.DecodeFrame(t.Context(), mustProtocolValue(t, `{"type":"response.started","responseId":"r","sequence":0}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := replay.Consume(header.Events[0]); err != nil || !accepted {
		t.Fatal("response opening was rejected", accepted, err)
	}
	for index, raw := range []string{
		`{"type":"item.started","responseId":"r","itemId":"i","item":{"kind":"text"},"sequence":7}`,
		`{"type":"item.delta","responseId":"r","itemId":"i","delta":"must not disappear","sequence":8}`,
		`{"type":"item.finished","responseId":"r","itemId":"i","sequence":9}`,
	} {
		value, err := protocol.ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		frame, err := compiled.DecodeFrame(t.Context(), value, options)
		if err != nil || len(frame.Events) != 1 {
			t.Fatal("source frame did not decode to one sequenced event", frame, err)
		}
		if index == 1 {
			// Wire lifecycle is valid. Model a post-decode projection that reuses
			// an earlier sequence: target semantic replay must detect the conflict.
			frame.Events[0].Sequence = mustProtocolValue(t, "7")
		}
		err = stream.consumeSourceFrame(frame, nil)
		if index < 2 {
			if err != nil || !state.Buffered {
				t.Fatal("event escaped the buffering conversion", index, err)
			}
			continue
		}
		// The finishing frame releases three projected members. The first two
		// reuse sequence 7 but differ semantically: using the finishing frame's
		// single wire digest for both would incorrectly discard the text delta.
		var conflict *protocol.ConversionError
		if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "sequence number was reused with different event content") {
			t.Fatal("expanded frame hid a reused-sequence conflict", err)
		}
	}
}

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
