package builtin

import (
	"bytes"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageTargetStreamMigration(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, sample := range legacy.Definition().Samples {
		if sample.Direction != p.EncodeEvent {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var events []p.Event
			if err := sample.Input.Decode(&events); err != nil {
				t.Fatal(err)
			}
			for _, isPrepared := range []bool{false, true} {
				beforeInput := events
				afterInput := streamEventsAtVersion(events, p.ProtocolCoreVersion)
				if isPrepared {
					beforeInput = prepareLegacyTargetUsage(t, legacy, events, sample.Scope)
					afterInput = prepareDeclaredTargetUsage(t, candidate, afterInput, sample.Scope)
				}
				before := encodeDeclaredTargetStream(t, legacy, beforeInput, sample, isPrepared)
				after := encodeDeclaredTargetStream(t, candidate, afterInput, sample, isPrepared)
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatalf("target frames changed (prepared=%v):\nold: %s\nnew: %s", isPrepared, before.JSONString(), after.JSONString())
				}
			}
		})
	}
}

func TestAnthropicPackageTargetStreamContinuations(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []struct {
		name, sample string
		transform    func([]p.Event) []p.Event
	}{
		{"late-tool-identity", "stream-tool-encode", func(events []p.Event) []p.Event {
			ready := events[1]
			node := *ready.Item
			ready.Type, ready.Item = p.ItemSnapshot, &node
			ready.Item.Input = &p.ToolInput{Kind: p.JSONInput}
			events[1].Item.Name, events[1].Item.CallID = p.Value{}, p.Value{}
			return append(events[:3:3], append([]p.Event{ready}, events[3:]...)...)
		}},
		{"text-snapshots", "stream-text-encode", func(events []p.Event) []p.Event {
			snapshot := events[2]
			snapshot.Type, snapshot.Delta = p.ItemSnapshot, p.Value{}
			snapshot.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("hi")}
			growth := snapshot
			growth.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("hi!")}
			return append(events[:3:3], append([]p.Event{snapshot, growth}, events[3:]...)...)
		}},
		{"automatic-tool-close", "stream-tool-encode", func(events []p.Event) []p.Event {
			return append(events[:3:3], events[4:]...)
		}},
		{"inferred-tool-finish", "stream-tool-encode", func(events []p.Event) []p.Event {
			events[len(events)-1].Response.Attributes = nil
			return events
		}},
		{"late-usage-metadata", "stream-text-encode", func(events []p.Event) []p.Event {
			events[len(events)-1].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "usage", Name: "service_tier", Path: "/usage/service_tier", Value: p.StringValue("standard")}}
			return append(events, p.Event{SchemaVersion: legacy.CoreVersion(), Type: p.MetadataUpdated, ResponseID: p.StringValue("r"), Metadata: []p.ResponseMetadata{{Codec: Anthropic, Location: "usage", Name: "service_tier", Path: "/usage/service_tier", Value: p.StringValue("priority")}}})
		}},
		{"frame-metadata", "stream-text-encode", func(events []p.Event) []p.Event {
			events[2].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "event:content_block_delta", Name: "extension", Path: "/extension", Value: testValue(t, `{"n":9007199254740993,"label":"\u0078"}`)}}
			return events
		}},
		{"terminal-metadata", "stream-text-encode", func(events []p.Event) []p.Event {
			events[4].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "event:message_delta", Name: "extension", Path: "/extension", Value: testValue(t, `{"n":9007199254740993}`)}}
			return events
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var sample p.Sample
			for _, entry := range legacy.Definition().Samples {
				if entry.ID == fixture.sample {
					sample = entry
					break
				}
			}
			var events []p.Event
			if err := sample.Input.Decode(&events); err != nil {
				t.Fatal(err)
			}
			events = fixture.transform(events)
			beforeInput := prepareLegacyTargetUsage(t, legacy, events, sample.Scope)
			afterInput := prepareDeclaredTargetUsage(t, candidate, streamEventsAtVersion(events, p.ProtocolCoreVersion), sample.Scope)
			before := encodeDeclaredTargetStream(t, legacy, beforeInput, sample, true)
			after := encodeDeclaredTargetStream(t, candidate, afterInput, sample, true)
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatalf("continuation changed:\nold: %s\nnew: %s", before.JSONString(), after.JSONString())
			}
		})
	}
}

func TestAnthropicPackageTargetMetadataRejections(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, metadata := range []p.ResponseMetadata{
		{Codec: Chat, Location: "response", Name: "service_tier", Path: "/choices/0/message/service_tier", Value: p.StringValue("standard")},
		{Codec: Anthropic, Location: "event:message_delta", Name: "extension", Path: "/extension", Value: p.StringValue("value")},
		{Codec: Anthropic, Location: "content", Name: "citations", Path: "/content/0/citations", Value: testValue(t, `[]`)},
	} {
		event := p.Event{SchemaVersion: legacy.CoreVersion(), Type: p.ResponseStarted, ResponseID: p.StringValue("r"),
			Response: &p.Response{SchemaVersion: legacy.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m")}, Metadata: []p.ResponseMetadata{metadata}}
		before := p.EvaluationContext{State: p.NewEvaluationState()}
		after := p.EvaluationContext{State: p.NewEvaluationState()}
		_, oldErr := legacy.EncodeFrames(t.Context(), event, before)
		converted := streamEventsAtVersion([]p.Event{event}, candidate.CoreVersion())[0]
		_, newErr := candidate.EncodeFrames(t.Context(), converted, after)
		before.State.Release()
		after.State.Release()
		checkPackageFailureRejection(t, oldErr, newErr)
	}
}

func TestAnthropicPackageTargetResourceOrigin(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	candidate := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	for _, source := range []*p.Provenance{nil, {Protocol: p.Identity{Family: "gemini", WireVersion: "v2"}}} {
		event := p.Event{SchemaVersion: legacy.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("reasoning"), Item: &p.Node{
			Kind: p.ReasoningNode, Source: source, Resources: []p.Resource{{Kind: "encrypted_content", ID: p.StringValue("sealed"), Scope: scope}},
		}}
		before := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
		after := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
		_, oldErr := legacy.EncodeFrames(t.Context(), event, before)
		event.SchemaVersion = candidate.CoreVersion()
		_, newErr := candidate.EncodeFrames(t.Context(), event, after)
		before.State.Release()
		after.State.Release()
		checkPackageFailureRejection(t, oldErr, newErr)
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

func encodeDeclaredTargetStream(t *testing.T, compiled *p.Compiled, events []p.Event, sample p.Sample, shouldValidate bool) p.Value {
	t.Helper()
	options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: p.NewEvaluationState()}
	defer options.State.Release()
	var validation *p.WireStreamValidation
	if shouldValidate {
		var err error
		validation, err = compiled.NewWireStreamValidation(sample.Scope)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, isPackage := compiled.Package()
	if isPackage && validation != nil {
		options = options.WithOutputStream(validation)
	}
	var frames []p.Value
	consume := func(batch []p.Value) {
		t.Helper()
		for _, frame := range batch {
			if validation != nil {
				if err := validation.Consume(t.Context(), frame); err != nil {
					t.Fatal("independent target wire contract", err, frame.JSONString())
				}
			}
		}
		if isPackage && validation != nil && len(batch) > 0 {
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
		frames = append(frames, batch...)
	}
	for _, event := range events {
		batch, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(event.Type, err)
		}
		consume(batch)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
	if validation != nil {
		if err := validation.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	output, err := p.EncodeValue(frames)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

// The historical baseline retains its own preparation policy. It is never
// used to supply the new package's target envelope.
func prepareLegacyTargetUsage(t *testing.T, target *p.Compiled, events []p.Event, scope p.Scope) []p.Event {
	t.Helper()
	source := shippedProjectionProtocol(t, Chat)
	policy := p.DefaultConversionPolicy(target, source)
	for _, rule := range policy.Rules {
		if rule.Phase == p.ConversionEvent && rule.Action == "anthropic_usage_envelope" {
			policy.Rules = []p.ConversionRule{rule}
			break
		}
	}
	if len(policy.Rules) != 1 {
		t.Fatal("legacy target preparation rule missing")
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	prepared := make([]p.Event, len(events))
	for index, event := range events {
		prepared[index], err = conversion.Event(t.Context(), event, p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Scope: scope}, &p.DiagnosticSink{})
		if err != nil {
			t.Fatal(err)
		}
	}
	return prepared
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
