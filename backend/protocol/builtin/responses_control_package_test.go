package builtin

import (
	"bytes"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageControlSourceStream(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	errors := responsesErrorEnvelopes()
	for _, fixture := range []struct {
		name   string
		frames []string
	}{
		{"keepalive", []string{`{"type":"keepalive"}`, progressStart, `{"type":"keepalive","sequence_number":1}`, progressEnd, `{"type":"keepalive","sequence_number":11}`}},
		{"nested-error", []string{errors[0]}},
		{"flat-error", []string{errors[1]}},
		{"failed-before-start", []string{`{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":{"type":"api_error","message":"failed"}}}`}},
		{"failed-string", []string{progressStart, `{"type":"response.failed","sequence_number":1,"response":{"id":"r","error":"failed"}}`}},
		{"failure-ignores-output-and-usage", []string{progressStart, `{"type":"response.failed","sequence_number":1,"response":{"id":"r","status":"anything","output":42,"usage":false,"error":{"type":"server_error","message":"failed"}}}`}},
		{"failure-metadata", []string{`{"type":"error","sequence_number":1,"message":"failed","service_tier":"default","metadata":{"n":9007199254740993},"content_filters":null}`}},
		{"partial-tool-failure", []string{progressStart,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"function_call","id":"tool","call_id":"call","name":"lookup","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"tool","delta":"{\"n\":"}`,
			`{"type":"error","sequence_number":3,"message":"interrupted"}`}},
		{"usage-after-failure", []string{errors[0], `{"type":"response.in_progress","sequence_number":52,"response":{"usage":{"input_tokens":1}}}`}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			sourceTarget := legacy.Target(p.DecodeEvent, p.Scope{})
			sourceTarget.Direction = p.EncodeEvent
			replay, err := p.NewEventReplay(sourceTarget, legacy.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			var oldEvents, newEvents []p.Event
			for index, raw := range fixture.frames {
				value := testValue(t, raw)
				before, err := legacy.DecodeFrame(t.Context(), value, oldOptions)
				if err != nil {
					t.Fatal("legacy control", index, err)
				}
				for _, event := range before.Events {
					if _, err := replay.Consume(event); err != nil {
						t.Fatal("legacy lifecycle", index, err)
					}
				}
				if err := wire.Consume(t.Context(), value); err != nil {
					t.Fatal("independent control contract", index, err)
				}
				after, err := candidate.DecodeFrame(t.Context(), value, newOptions)
				if err != nil {
					t.Fatal("declared control", index, err)
				}
				want, err := p.EncodeValue(responsesTextEventsAtOriginalLocations(t, before.Events, value))
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.EncodeValue(after.Events)
				if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatalf("control event changed at %d: %v\nold: %s\nnew: %s", index, err, want.JSONString(), got.JSONString())
				}
				oldEvents, newEvents = append(oldEvents, before.Events...), append(newEvents, after.Events...)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			if err := replay.Finish(); err != nil {
				t.Fatal(err)
			}
			if fixture.name == "nested-error" || fixture.name == "flat-error" {
				for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
					want := encodeMigratedStream(t, target, oldEvents, p.Scope{})
					got := encodeMigratedStream(t, target, streamEventsAtVersion(newEvents, p.SemanticSchemaVersion), p.Scope{})
					if !bytes.Equal(want.Bytes(), got.Bytes()) {
						t.Fatal("failure reconstruction changed", target, want.JSONString(), got.JSONString())
					}
				}
			}
		})
	}
}

func TestResponsesPackageControlTerminalBoundary(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"error","sequence_number":11,"message":"too late"}`,
		`{"type":"response.failed","sequence_number":11,"response":{"id":"r","error":{"message":"too late"}}}`,
		`{"type":"keepalive","sequence_number":11,"vendor":42}`,
		`{"type":"keepalive","sequence_number":10}`,
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		sourceTarget := legacy.Target(p.DecodeEvent, p.Scope{})
		sourceTarget.Direction = p.EncodeEvent
		replay, err := p.NewEventReplay(sourceTarget, legacy.ResourceLimits())
		if err != nil {
			t.Fatal(err)
		}
		decodeLegacy := func(value p.Value) error {
			frame, err := legacy.DecodeFrame(t.Context(), value, options)
			if err != nil {
				return err
			}
			for _, event := range frame.Events {
				if _, err := replay.Consume(event); err != nil {
					return legacy.AnnotateSourceEventError(err)
				}
			}
			return nil
		}
		for _, prefix := range []string{progressStart, progressEnd} {
			value := testValue(t, prefix)
			if err := decodeLegacy(value); err != nil {
				t.Fatal(err)
			}
			if err := wire.Consume(t.Context(), value); err != nil {
				t.Fatal(err)
			}
		}
		value := testValue(t, raw)
		checkPackageFailureRejection(t, decodeLegacy(value), wire.Consume(t.Context(), value))
	}
	validation, err := candidate.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), testValue(t, `{"type":"keepalive","sequence_number":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := validation.Finish(); err == nil {
		t.Fatal("heartbeat-only EOF invented a terminal")
	}
}

func TestResponsesPackageControlRejections(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, fixture := range responsesErrorRejections() {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		value := testValue(t, fixture.body)
		_, before := legacy.DecodeFrame(t.Context(), value, options)
		after := wire.Consume(t.Context(), value)
		checkPackageFailureRejection(t, before, after)
	}
	for _, raw := range []string{
		`{"type":"keepalive","sequence_number":null}`,
		`{"type":"keepalive","sequence_number":"1"}`,
		`{"type":"error","sequence_number":1,"error":{"type":"","message":"failed"}}`,
		`{"type":"response.failed","sequence_number":1,"response":{"error":null}}`,
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		value := testValue(t, raw)
		_, before := legacy.DecodeFrame(t.Context(), value, options)
		after := wire.Consume(t.Context(), value)
		checkPackageFailureRejection(t, before, after)
	}
}

func TestResponsesPackageControlPreservesExtensions(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"keepalive","sequence_number":1,"delta":"private"}`,
		`{"type":"keepalive","sequence_number":1,"service_tier":"default"}`,
		`{"type":"keepalive","sequence_number":1,"response":{"id":"r"}}`,
		`{"type":"error","sequence_number":1,"message":"failed","vendor":false}`,
	} {
		beforeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		afterOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		defer beforeOptions.State.Release()
		defer afterOptions.State.Release()
		value := testValue(t, raw)
		before, err := legacy.DecodeFrame(t.Context(), value, beforeOptions)
		if err != nil || len(before.Events) != 1 || before.Events[0].Unmapped == nil {
			t.Fatal("legacy fixture must preserve the unknown payload", before, err)
		}
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.Consume(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		after, err := candidate.DecodeFrame(t.Context(), value, afterOptions)
		if err != nil || len(after.Events) != 1 || after.Events[0].Type != before.Events[0].Type || after.Events[0].Unmapped == nil || len(after.Events[0].Metadata) != len(before.Events[0].Metadata) || after.Events[0].Error != before.Events[0].Error {
			t.Fatal("control lost or reclassified its payload", before, after, err)
		}
		expected, err := sonic.ConfigStd.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		unmapped := after.Events[0].Unmapped
		if unmapped.Source.Path != "" || unmapped.Source.Protocol != candidate.Identity() || unmapped.Source.Direction != p.DecodeEvent || !bytes.Equal(unmapped.Value.Bytes(), expected) {
			t.Fatal("control extension lost its original frame", unmapped)
		}
	}
}
