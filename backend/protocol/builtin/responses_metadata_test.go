package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const syntheticResponsesMetadata = `{"openai_chatgpt_moderation_metadata":{"generation":{},"prompt":{"omnimod":{"outputs":[{"protection_type":"fixture","number":9007199254740993}]}}},"escaped":"\u0061"}`

func TestResponsesMetadataAfterCannotEmitInvalidEnvelope(t *testing.T) {
	document, _ := testCompiled(t, Responses).Package()
	document.Spec.ID = "metadata-invalid-output"
	invalid := testValue(t, `{"type":"response.metadata","metadata":null,"response_id":"r","sequence_number":2}`)
	batch, err := p.EncodeValue([]p.Value{invalid})
	if err != nil {
		t.Fatal(err)
	}
	mapping := document.Mapping.Directions[p.EncodeEvent]
	condition := *mapping.Program.When
	mapping.Program.When = &p.Expression{Op: "any", Items: []p.Expression{condition, {Op: "equal", Items: []p.Expression{{Op: "read", Path: "/type"}, {Op: "literal", Value: p.StringValue(string(p.MetadataUpdated))}}}}}
	mapping.Program.Then = &p.Expression{Op: "literal", Value: batch}
	document.Mapping.Directions[p.EncodeEvent] = mapping
	compiled, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	frames, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.MetadataUpdated}, options)
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != "/metadata" || failure.Issues[0].Direction != p.EncodeEvent || len(frames) != 0 {
		t.Fatal("authored invalid output did not fail at its wire field", frames, err)
	}
	validator, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validator.Release()
	start := testValue(t, `{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","created_at":1,"model":"m","status":"in_progress","output":[]}}`)
	if err := validator.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	err = validator.Consume(t.Context(), invalid)
	if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != "/metadata" || failure.Issues[0].Direction != p.EncodeEvent || failure.Issues[0].Protocol != compiled.Identity() {
		t.Fatal("authored invalid output bypassed final validation", err)
	}
	decode := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer decode.State.Release()
	if _, err := compiled.DecodeFrame(t.Context(), start, decode); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), invalid, decode); err == nil {
		t.Fatal("source metadata accepted the same invalid envelope")
	}
}

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
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope})
	for _, sample := range packageMapping(compiled).Samples {
		if sample.ID != "declared-stream-text-decode" {
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
				out, err := to.EncodeConvertedFrame(t.Context(), frame, []p.Event{event}, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
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
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
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
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})); err == nil {
		t.Fatal("metadata established a response")
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	start, err := compiled.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), options)
	if err != nil {
		t.Fatal(err)
	}
	rejected := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, strings.Replace(valid, `"response_id":"r"`, `"response_id":"other"`, 1)), rejected); err == nil {
		t.Fatal("metadata switched response identity")
	}
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, valid), options); err == nil {
		t.Fatal("repeated metadata sequence accepted")
	}
	collector, _ := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), compiled.ResourceLimits())
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
		"response":      func(e *p.Event) { e.Response = &p.Response{SchemaVersion: p.ProtocolCoreVersion} },
		"item":          func(e *p.Event) { e.Item = &p.Node{Kind: p.TextNode} },
		"error":         func(e *p.Event) { e.Error = p.StringValue("failure") },
		"media":         func(e *p.Event) { e.Media = &p.Media{} },
		"request":       func(e *p.Event) { e.Request = &p.Request{SchemaVersion: p.ProtocolCoreVersion} },
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
	native.Source.Path = "/unrelated"
	event.Native = &native
	if _, err := conversion.Event(t.Context(), event, route, nil); err == nil {
		t.Fatal("modified native provenance was accepted")
	}
}

func TestResponsesMetadataCollectorRequiresStartedIdentity(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	event := decodeResponsesMetadataFixture(t, compiled, p.Scope{}).Events[0]
	for _, test := range []struct {
		name    string
		startID p.Value
		isValid bool
	}{
		{"before-start", p.Value{}, false},
		{"other-response", p.StringValue("other"), false},
		{"matching-response", event.ResponseID, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector, _ := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), compiled.ResourceLimits())
			if !test.startID.IsZero() {
				start := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, ResponseID: test.startID}
				if _, _, err := collector.Consume(start); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := collector.Consume(event); (err == nil) != test.isValid {
				t.Fatalf("metadata acceptance = %v, want %t", err, test.isValid)
			}
		})
	}
}

func TestResponsesMetadataCollectorFrameLimits(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	if _, err := compiled.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), options); err != nil {
		t.Fatal(err)
	}
	frame, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.metadata","metadata":{},"response_id":"r","sequence_number":2}`), options)
	if err != nil {
		t.Fatal(err)
	}
	event := frame.Events[0]
	frameBytes := len(event.Native.Value.JSONString())
	for _, test := range []struct {
		name                string
		bytes, nodes, depth int
		metric              string
	}{
		{"exact-limits", frameBytes, 11, 2, ""},
		{"bytes", frameBytes - 1, 11, 2, "bytes"},
		{"nodes", frameBytes, 10, 2, "nodes"},
		{"depth", frameBytes, 11, 1, "depth"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := compiled.ResourceLimits()
			limits.BufferBytes, limits.Nodes, limits.Depth = test.bytes, test.nodes, test.depth
			collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := collector.Consume(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, ResponseID: event.ResponseID}); err != nil {
				t.Fatal(err)
			}
			_, _, err = collector.Consume(event)
			if test.metric == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *p.ResourceLimitError
			if !errors.As(err, &failure) || failure.Metric != test.metric || failure.Observed <= failure.Limit {
				t.Fatalf("metadata resource failure = %v, want %s limit", err, test.metric)
			}
		})
	}
}

func TestResponsesMetadataNativeReplayAndCollection(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	for _, sample := range packageMapping(compiled).Samples {
		if sample.ID != "declared-stream-text-decode" && sample.ID != "stream-tool-decode" && sample.ID != "encrypted-stream-reasoning-decode" {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Provider: "fixture", Account: "fixture"}})
			collector, _ := p.NewResponseCollector(compiled.Target(p.EncodeEvent, options.Scope), compiled.ResourceLimits())
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

func TestResponsesMetadataRejectsPostTerminalFrames(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	for _, sample := range packageMapping(compiled).Samples {
		if sample.ID != "declared-stream-text-decode" {
			continue
		}
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
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
