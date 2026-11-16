package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageTargetMetadataRejections(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	for _, metadata := range []p.ResponseMetadata{
		{Codec: Chat, Location: "response", Name: "service_tier", Path: "/choices/0/message/service_tier", Value: p.StringValue("standard")},
		{Codec: Anthropic, Location: "event:message_delta", Name: "extension", Path: "/extension", Value: p.StringValue("value")},
		{Codec: Anthropic, Location: "content", Name: "citations", Path: "/content/0/citations", Value: testValue(t, `[]`)},
	} {
		event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, ResponseID: p.StringValue("r"),
			Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Usage: &p.Usage{Input: &p.Counter{Origin: p.ObservedCount}, Output: &p.Counter{Origin: p.ObservedCount}}}, Metadata: []p.ResponseMetadata{metadata}}
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		_, err := compiled.EncodeFrames(t.Context(), event, options)
		var failure *p.ConversionError
		path := metadata.Path
		if metadata.Location == "content" {
			path = "/item/metadata"
		}
		if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != path {
			t.Fatal("unrepresented event metadata lost its explicit rejection", metadata, err)
		}
	}
}

func TestAnthropicPackageTargetResourceOrigin(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	for _, source := range []*p.Provenance{nil, {Protocol: p.Identity{Family: "gemini", WireVersion: "v2"}}} {
		event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("reasoning"), Item: &p.Node{
			Kind: p.ReasoningNode, Source: source, Resources: []p.Resource{{Kind: "encrypted_content", ID: p.StringValue("sealed"), Scope: scope}},
		}}
		options := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
		defer options.State.Release()
		_, err := compiled.EncodeFrames(t.Context(), event, options)
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != "/resources" {
			t.Fatal("unowned resource reached target encoding", source, err)
		}
	}
}

func TestAnthropicPackageFinalEventContract(t *testing.T) {
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []finalWireCase{
		{"start", `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}`, acceptedFinalWire},
		{"missing-usage", `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[]}}`, "/message/usage"},
		{"missing-identity-before-usage", `{"type":"message_start","message":{"model":"m","type":"message","role":"assistant","content":[]}}`, "/message/id"},
		{"early-error", `{"type":"error","error":{}}`, acceptedFinalWire},
		{"string-error", `{"type":"error","error":"failed"}`, "/error"},
		{"missing-type", `{}`, "/type"},
		{"unknown-type", `{"type":"unknown\"\n"}`, "/type"},
		{"thinking-start-needs-no-signature", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, acceptedFinalWire},
		{"decimal-index", `{"type":"content_block_stop","index":1.0}`, "/index"},
		{"signature-delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed"}}`, acceptedFinalWire},
		{"terminal", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, acceptedFinalWire},
		{"missing-stop-before-usage", `{"type":"message_delta","delta":{},"usage":null}`, "/delta/stop_reason"},
		{"null-usage", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":null}`, "/usage"},
	} {
		t.Run(fixture.name, func(t *testing.T) { checkFinalWireCase(t, candidate, p.EncodeEvent, fixture) })
	}
}

func encodeDeclaredTargetStream(t *testing.T, compiled *p.Compiled, events []p.Event, sample p.Sample) p.Value {
	t.Helper()
	frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.EncodeValue(frames)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func prepareDeclaredTargetUsage(t *testing.T, target *p.Compiled, events []p.Event, scope p.Scope) []p.Event {
	t.Helper()
	source := compileBuiltinPackage(t, "openai-chat")
	policy := declaredRulePolicy(t, target, source, "event-anthropic-usage-envelope", "compatible")
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Scope: scope, Transport: p.SSE}
	prepared := make([]p.Event, len(events))
	for index, event := range events {
		// These direct encoder fixtures author one event per source frame.
		// Real compound-frame fixtures bind the complete decoded batch instead.
		frameRoute, err := conversion.WithFrameUsage(route, []p.Event{event})
		if err != nil {
			t.Fatal(err)
		}
		prepared[index], err = conversion.Event(t.Context(), event, frameRoute, &p.DiagnosticSink{})
		if err != nil {
			t.Fatal(err)
		}
	}
	return prepared
}
