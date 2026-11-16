package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageControlPreservesExtensions(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"keepalive","sequence_number":1,"delta":"private"}`,
		`{"type":"keepalive","sequence_number":1,"service_tier":"default"}`,
		`{"type":"keepalive","sequence_number":1,"response":{"id":"r"}}`,
		`{"type":"error","sequence_number":1,"message":"failed","vendor":false}`,
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		defer validation.Release()
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil || len(frame.Events) != 1 {
			t.Fatal("control extension disappeared", frame, err)
		}
		assertResponsesUnmappedFrame(t, compiled, frame.Events[0], raw)
	}
}

func TestResponsesPackageProgressPreservesExtensions(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","created":9007199254740993}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"prompt_cache_hit_tokens":9007199254740993}}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","tool_usage":{"vendor":{"unit":9007199254740993}}}}`,
	} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		defer validation.Release()
		if _, err := validation.DecodeFrame(t.Context(), testValue(t, progressStart), options); err != nil {
			t.Fatal(err)
		}
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil || len(frame.Events) != 1 {
			t.Fatal("progress extension disappeared", frame, err)
		}
		assertResponsesUnmappedFrame(t, compiled, frame.Events[0], raw)
	}
}

func assertResponsesUnmappedFrame(t *testing.T, compiled *p.Compiled, event p.Event, raw string) {
	t.Helper()
	unmapped := event.Unmapped
	if unmapped == nil || unmapped.Source.Path != "" || unmapped.Source.Protocol != compiled.Identity() || unmapped.Source.Direction != p.DecodeEvent {
		t.Fatal("unknown fields lost their source owner", unmapped)
	}
	sameJSON(t, unmapped.Value.Bytes(), raw)
}

func TestResponsesPackageProgressReplacesLargeUsageField(t *testing.T) {
	const fieldBytes = 4 << 20
	compiled := compileBuiltinPackage(t, "openai-responses")
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	replay, err := p.NewEventReplay(compiled.Target(p.EncodeEvent, p.Scope{}), compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Release()
	for index, raw := range []string{
		progressStart,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"vendor":"` + strings.Repeat("x", fieldBytes) + `"}}}`,
		`{"type":"response.in_progress","sequence_number":2,"response":{"id":"r","usage":{"vendor":"` + strings.Repeat("y", fieldBytes) + `"}}}`,
		progressEnd,
	} {
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal("replacement retained both complete fields", index, err)
		}
		for _, event := range frame.Events {
			if _, err := replay.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
		if index == 1 || index == 2 {
			if len(frame.Events) != 1 {
				t.Fatal("usage replacement split its observation", frame.Events)
			}
			assertResponsesUnmappedFrame(t, compiled, frame.Events[0], raw)
		}
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
}
