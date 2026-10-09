package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatSingleChoiceExtensionKeepsNativeBoundary(t *testing.T) {
	source := shippedProjectionProtocol(t, Chat)
	// A real channel triggered a nil-map panic with native_finish_reason.
	// Use an unknown vendor key here to retain the extension boundary itself.
	wire := `{"id":"r","model":"m","created":1,"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop","vendor_finish":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	response, err := source.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := source.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(encoded), `"vendor_finish":"stop"`) {
		t.Fatalf("native extension lost: %s %v", encoded, err)
	}
	target := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := conversion.Response(t.Context(), response, p.ConversionContext{}, nil)
	if err == nil {
		_, err = target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
	}
	if err == nil {
		t.Fatal("unknown choice extension silently crossed protocol boundary")
	}
}

func TestResponsesProgressMetadataDoesNotInventUsage(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	validator, err := compiled.NewWireStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[],"usage":null,"service_tier":"auto"}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[],"usage":null,"service_tier":"auto"}}`,
		`{"type":"response.completed","sequence_number":2,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2},"service_tier":"auto"}}`,
	} {
		value := testValue(t, wire)
		frame, err := compiled.DecodeFrame(t.Context(), value, options)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(wire, `"type":"response.in_progress"`) {
			if len(frame.Events) != 1 || frame.Events[0].Type != p.MetadataUpdated || frame.Events[0].Usage != nil {
				t.Fatalf("metadata was represented as billing: %+v", frame.Events)
			}
			for _, codec := range []string{Chat, Anthropic, Gemini} {
				target := shippedProjectionProtocol(t, codec)
				conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(target, compiled))
				projected, err := conversion.Event(t.Context(), frame.Events[0], p.ConversionContext{}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = p.IssuesError(p.CheckEvent(projected, p.Target{Protocol: target.Identity(), Direction: p.EncodeEvent, Capabilities: target.Capabilities(p.EncodeEvent)}, p.DefaultLimits())); err != nil {
					t.Fatalf("%s metadata projection rejected: %v", codec, err)
				}
				if projected.Usage != nil {
					t.Fatal("metadata projection invented usage")
				}
			}
		}
		if err = validator.Consume(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	if err = validator.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(p.CheckEvent(p.Event{SchemaVersion: 1, Type: p.UsageUpdated}, p.Target{}, p.DefaultLimits())) == 0 {
		t.Fatal("missing usage was globally accepted")
	}
}
