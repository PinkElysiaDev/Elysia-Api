package builtin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const syntheticResponsesMetadata = `{"openai_chatgpt_moderation_metadata":{"generation":{},"prompt":{"omnimod":{"outputs":[{"protection_type":"fixture","number":9007199254740993}]}}},"escaped":"\u0061"}`

func responsesMetadataFrames(t *testing.T, sample p.Sample) []p.Value {
	t.Helper()
	var frames []p.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	first, _ := frames[0].ReadObject()
	response, _ := first["response"].ReadObject()
	for i, frame := range frames {
		fields, _ := frame.ReadObject()
		fields["sequence_number"], _ = p.EncodeValue(i)
		frames[i], _ = p.EncodeValue(fields)
	}
	last := len(frames) - 1
	metadata := testValue(t, fmt.Sprintf(` {"metadata":%s,"response_id":%s,"sequence_number":%d,"type":"response.metadata"} `, syntheticResponsesMetadata, response["id"].JSONString(), last))
	terminal, _ := frames[last].ReadObject()
	terminal["sequence_number"], _ = p.EncodeValue(last + 1)
	frames[last], _ = p.EncodeValue(terminal)
	return append(frames[:last], metadata, frames[last])
}

func decodeResponsesMetadataFixture(t *testing.T, compiled *p.Compiled, scope p.Scope) *p.EventFrame {
	t.Helper()
	options := p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}
	for _, sample := range compiled.Definition().Samples {
		if sample.ID != "stream-text-decode" {
			continue
		}
		frames := responsesMetadataFrames(t, sample)
		for _, input := range frames[:len(frames)-1] {
			frame, err := compiled.DecodeFrame(t.Context(), input, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(frame.Events) == 1 && frame.Events[0].Type == p.NativeEvent {
				return frame
			}
		}
	}
	t.Fatal("metadata fixture missing")
	return nil
}

func TestResponsesMetadataPolicies(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	frame := decodeResponsesMetadataFixture(t, from, p.Scope{})
	original, _ := p.EncodeValue(frame.Events[0])
	for _, codec := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict", "disabled", "json-condition"} {
			t.Run(codec+"/"+mode, func(t *testing.T) {
				policy := p.DefaultConversionPolicy(to, from)
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for i := range policy.Rules {
						if policy.Rules[i].ID == "event-metadata" {
							policy.Rules[i].Enabled = false
						}
					}
				}
				if mode == "json-condition" {
					for i := range policy.Rules {
						if policy.Rules[i].ID == "event-metadata" {
							hasType := true
							policy.Rules[i].Match.Path = "/type"
							policy.Rules[i].Match.Present = &hasType
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				event, err := conversion.Event(t.Context(), frame.Events[0], conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE), sink)
				if codec != Responses && mode == "strict" {
					if err == nil || !strings.Contains(err.Error(), "conversion_rejected") {
						t.Fatal("strict omission accepted", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				out, err := to.EncodeConvertedFrame(t.Context(), frame, []p.Event{event}, p.EvaluationContext{State: p.NewEvaluationState()})
				if codec != Responses && mode == "disabled" {
					if err == nil {
						t.Fatal("disabled omission accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if codec == Responses {
					if len(out) != 1 || out[0] != frame.Events[0].Native.Value || len(sink.Issues()) != 0 {
						t.Fatal("same-wire metadata changed")
					}
				} else {
					if len(out) != 0 || event.Type != p.MetadataUpdated || event.Native != nil || len(sink.Issues()) != 1 {
						t.Fatal("metadata was not a diagnosed no-op")
					}
					issue := sink.Issues()[0]
					if issue.Code != p.ConversionDegraded || issue.Fidelity != "lossy_compatible" || issue.Path != "/metadata" || issue.RuleID != "event-metadata" || issue.PolicyHash != conversion.Hash || issue.Evidence == "" || issue.Stage != "conversion.event" || strings.Contains(issue.Reason, "protection_type") {
						t.Fatal("incomplete or leaking diagnostic", issue)
					}
				}
				current, _ := p.EncodeValue(frame.Events[0])
				if current != original {
					t.Fatal("conversion changed caller-owned input")
				}
			})
		}
	}
}

func TestResponsesMetadataInvalidEnvelopesAndLifecycle(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	valid := `{"type":"response.metadata","metadata":{},"response_id":"r","sequence_number":2}`
	for _, raw := range []string{
		strings.Replace(valid, `"metadata":{}`, `"metadata":null`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":[]`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":"x"`, 1),
		strings.Replace(valid, `"metadata":{},`, ``, 1),
		strings.Replace(valid, `"response_id":"r"`, `"response_id":""`, 1),
		strings.Replace(valid, `"response_id":"r"`, `"response_id":null`, 1),
		strings.Replace(valid, `"response_id":"r"`, `"response_id":12`, 1),
		strings.Replace(valid, `"response_id":"r",`, ``, 1),
		strings.Replace(valid, `,"sequence_number":2`, ``, 1),
		strings.Replace(valid, `"sequence_number":2`, `"sequence_number":null`, 1),
		strings.Replace(valid, `"sequence_number":2`, `"sequence_number":-1`, 1),
		strings.Replace(valid, `"sequence_number":2`, `"sequence_number":2.5`, 1),
		strings.Replace(valid, `"sequence_number":2`, `"sequence_number":"2"`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":{},"metadata":{}`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":{},"\u006detadata":{}`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":{},"headers":null`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":{},"headers":{"x-codex-turn-state":"synthetic"}`, 1),
		strings.Replace(valid, `"metadata":{}`, `"metadata":{},"delta":"content"`, 1),
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), options); err != nil {
			t.Fatal(err)
		}
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options); err == nil {
			t.Fatal("invalid envelope accepted", raw)
		}
		if err := compiled.ValidateWireOutput(p.EncodeEvent, testValue(t, raw)); err == nil {
			t.Fatal("invalid after output accepted", raw)
		}
	}
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
		t.Fatal("metadata established a response")
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	start, err := compiled.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, strings.Replace(valid, `"response_id":"r"`, `"response_id":"other"`, 1)), options); err == nil {
		t.Fatal("metadata switched response identity")
	}
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), options); err == nil {
		t.Fatal("repeated metadata sequence accepted")
	}
	collector, _ := p.NewResponseCollector(p.Target{Codec: Responses, Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, compiled.ResourceLimits())
	for _, event := range append(start.Events, frame.Events...) {
		if _, _, err := collector.Consume(event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collector.Finish(); err == nil {
		t.Fatal("metadata fabricated response completion")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.DecodeFrame(ctx, testValue(t, valid), options); err == nil {
		t.Fatal("metadata ignored cancellation")
	}
}

func TestResponsesMetadataRejectsSemanticContamination(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	scope := p.Scope{Account: "source"}
	original := decodeResponsesMetadataFixture(t, from, scope).Events[0]
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	route := conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE)
	route.Scope = scope
	for name, mutate := range map[string]func(*p.Event){
		"delta":         func(e *p.Event) { e.Delta = p.StringValue("content") },
		"usage":         func(e *p.Event) { e.Usage = &p.Usage{} },
		"response":      func(e *p.Event) { e.Response = &p.Response{SchemaVersion: 1} },
		"item":          func(e *p.Event) { e.Item = &p.Node{Kind: p.TextNode} },
		"error":         func(e *p.Event) { e.Error = p.StringValue("failure") },
		"media":         func(e *p.Event) { e.Media = &p.Media{} },
		"request":       func(e *p.Event) { e.Request = &p.Request{SchemaVersion: 1} },
		"metadata":      func(e *p.Event) { e.Metadata = []p.ResponseMetadata{{Name: "unmapped"}} },
		"session":       func(e *p.Event) { e.SessionID = p.StringValue("session") },
		"sequence":      func(e *p.Event) { e.Sequence = testValue(t, `999`) },
		"source":        func(e *p.Event) { e.Source.Family = "unrelated" },
		"native-source": func(e *p.Event) { e.Native.Source.Protocol.Family = "unrelated" },
		"source-pair": func(e *p.Event) {
			e.Source.Family = "unrelated"
			e.Native.Source.Protocol = e.Source
		},
		"scope":     func(e *p.Event) { e.Native.Source.Scope.Account = "other" },
		"direction": func(e *p.Event) { e.Native.Source.Direction = p.DecodeResponse },
		"request-context": func(e *p.Event) {
			e.Native.RequestContext = &p.NativeRequestContext{}
		},
		"response-id": func(e *p.Event) { e.ResponseID = p.StringValue("other") },
		"unmapped":    func(e *p.Event) { copy := *e.Native; e.Unmapped = &copy },
	} {
		t.Run(name, func(t *testing.T) {
			event := original
			native := *event.Native
			event.Native = &native
			mutate(&event)
			if _, err := conversion.Event(t.Context(), event, route, nil); err == nil {
				t.Fatal("metadata projection discarded unrelated or forged data")
			}
		})
	}
	event := original
	native := *event.Native
	native.Source.Path = ""
	event.Native = &native
	projected, err := conversion.Event(t.Context(), event, route, nil)
	if err != nil || projected.Type != p.NativeEvent || projected.Native == nil {
		t.Fatal("unrecognized native event was silently omitted", err)
	}
	if _, err := to.EncodeFrames(t.Context(), projected, p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}); err == nil {
		t.Fatal("unrecognized native event crossed protocols")
	}
}

func TestResponsesMetadataNativeReplayAndCollection(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	for _, sample := range compiled.Definition().Samples {
		if sample.ID != "stream-text-decode" && sample.ID != "stream-tool-decode" && sample.ID != "encrypted-stream-reasoning-decode" {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Provider: "fixture", Account: "fixture"}}
			collector, _ := p.NewResponseCollector(p.Target{Codec: Responses, Protocol: compiled.Identity(), Direction: p.EncodeEvent, Scope: options.Scope, Capabilities: compiled.Capabilities(p.EncodeEvent)}, compiled.ResourceLimits())
			validator, _ := compiled.NewWireStreamValidation(options.Scope)
			for _, input := range responsesMetadataFrames(t, sample) {
				decoded, err := compiled.DecodeFrame(t.Context(), input, options)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range decoded.Events {
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal(err)
					}
				}
				output, err := compiled.EncodeFrame(t.Context(), decoded, options)
				if err != nil || len(output) != 1 || output[0] != input {
					t.Fatalf("native frame changed: %v", err)
				}
				if err := validator.Consume(t.Context(), output[0]); err != nil {
					t.Fatal(err)
				}
			}
			if err := validator.Finish(); err != nil {
				t.Fatal(err)
			}
			result, err := collector.Finish()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := p.EncodeValue(result)
			if strings.Contains(raw.JSONString(), "openai_chatgpt_moderation_metadata") {
				t.Fatal("auxiliary metadata entered generated result")
			}
		})
	}
}

func TestResponsesMetadataCustomMappingsAndLimits(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	compile := func(definition p.Definition) *p.Compiled {
		t.Helper()
		raw, _ := p.EncodeValue(definition)
		compiled, issues := compiler.Compile(raw.Bytes())
		if compiled == nil {
			t.Fatal(issues)
		}
		return compiled
	}
	frame := decodeResponsesMetadataFixture(t, from, p.Scope{})
	definition := from.Definition()
	definition.ID = "metadata-responses-copy"
	copy := compile(definition)
	out, err := copy.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil || len(out) != 1 || out[0] != frame.Events[0].Native.Value {
		t.Fatal("definition ID prevented equivalent native replay", err)
	}
	mapping := definition.Directions[p.EncodeEvent]
	mapping.After = &p.Expression{Op: "read", From: "input"}
	definition.Directions[p.EncodeEvent] = mapping
	withAfter := compile(definition)
	if _, err := withAfter.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
		t.Fatal("native replay bypassed a different after mapping")
	}
	definition = from.Definition()
	definition.ID = "handwritten-metadata"
	event, _ := p.EncodeValue(p.Event{SchemaVersion: 1, Type: p.NativeEvent, ResponseID: frame.Events[0].ResponseID, Sequence: frame.Events[0].Sequence})
	definition.Directions[p.DecodeEvent] = p.Mapping{Transform: &p.Expression{Op: "array", Items: []p.Expression{{Op: "literal", Value: event}}}}
	handwritten := compile(definition)
	decoded, err := handwritten.DecodeFrame(t.Context(), frame.Events[0].Native.Value, p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Events[0].Native.Source.Path == "/metadata" {
		t.Fatal("handwritten mapping minted the auxiliary provenance marker")
	}
	to := shippedProjectionProtocol(t, Chat)
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, handwritten))
	projected, err := conversion.Event(t.Context(), decoded.Events[0], conversion.VerificationRoute(handwritten.Identity(), to.Identity(), p.SSE), nil)
	if err != nil || projected.Type != p.NativeEvent || projected.Native == nil {
		t.Fatal("handwritten native metadata silently dropped", err)
	}
	definition = from.Definition()
	definition.ID = "bounded-metadata"
	limits := p.DefaultLimits()
	limits.BufferBytes = 4096
	definition.Limits = &limits
	bounded := compile(definition)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	if _, err := bounded.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), options); err != nil {
		t.Fatal(err)
	}
	large := testValue(t, `{"type":"response.metadata","response_id":"r","sequence_number":1,"metadata":{"payload":"`+strings.Repeat("x", limits.BufferBytes)+`"}}`)
	if _, err := bounded.DecodeFrame(t.Context(), large, options); err == nil {
		t.Fatal("metadata bypassed the frame budget")
	}
}

func TestResponsesMetadataRejectsPostTerminalFrames(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	for _, sample := range compiled.Definition().Samples {
		if sample.ID != "stream-text-decode" {
			continue
		}
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		validator, _ := compiled.NewWireStreamValidation(p.Scope{})
		frames := responsesMetadataFrames(t, sample)
		for _, input := range frames {
			if _, err := compiled.DecodeFrame(t.Context(), input, options); err != nil {
				t.Fatal(err)
			}
			if err := validator.Consume(t.Context(), input); err != nil {
				t.Fatal(err)
			}
		}
		metadata, _ := frames[len(frames)-2].ReadObject()
		metadata["sequence_number"], _ = p.EncodeValue(len(frames))
		after, _ := p.EncodeValue(metadata)
		if _, err := compiled.DecodeFrame(t.Context(), after, options); err == nil {
			t.Fatal("post-terminal metadata accepted by source")
		}
		if err := validator.Consume(t.Context(), after); err == nil {
			t.Fatal("post-terminal metadata accepted by final wire validation")
		}
	}
}
