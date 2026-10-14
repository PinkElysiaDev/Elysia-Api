package builtin

import (
	"context"
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const syntheticTurnStateFrame = `{"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic-turn-state"}}`

func TestResponsesTurnStateCompleteStreams(t *testing.T) {
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			frames := responsesPaddedTextFrames()
			frames = append([]string{syntheticTurnStateFrame}, frames...)
			frames = append(frames[:4], append([]string{syntheticTurnStateFrame}, frames[4:]...)...)
			auditConvertFrames(t, Responses, target, frames)
		})
	}
}

func TestResponsesTurnStateNativeReplayAndCollection(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	var sample p.Sample
	for _, candidate := range compiled.Definition().Samples {
		if candidate.ID == "stream-text-decode" {
			sample = candidate
		}
	}
	var frames []p.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	control := testValue(t, ` {"headers":{"X-Codex-Turn-State":"synthetic-\u0074urn"},"type":"response.metadata"} `)
	frames = append([]p.Value{control}, frames...)
	frames = append(frames[:2], append([]p.Value{control}, frames[2:]...)...)
	options := p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "a"}}
	collector, err := p.NewResponseCollector(p.Target{Codec: Responses, Protocol: compiled.Identity(), Direction: p.EncodeEvent, Scope: options.Scope, Capabilities: compiled.Capabilities(p.EncodeEvent)}, compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	validator, _ := compiled.NewWireStreamValidation(options.Scope)
	for _, input := range frames {
		decoded, err := compiled.DecodeFrame(t.Context(), input, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range decoded.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
			if event.ResponsesTurnState != nil && (!event.ResponseID.IsZero() || event.Usage != nil || len(event.Metadata) != 0) {
				t.Fatal("control state entered generation")
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
	if strings.Contains(string(raw.Bytes()), "turn-state") || strings.Contains(string(raw.Bytes()), "synthetic-") {
		t.Fatal("control state entered generated result")
	}
	if _, err := compiled.DecodeFrame(t.Context(), control, options); err == nil {
		t.Fatal("control accepted after terminal")
	}

	// Neither a control-only stream nor an interrupted one completes a response.
	for _, end := range []error{nil, context.Canceled} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		frame, err := compiled.DecodeFrame(t.Context(), control, options)
		if err != nil {
			t.Fatal(err)
		}
		collector, _ := p.NewResponseCollector(p.Target{Codec: Responses, Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, compiled.ResourceLimits())
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
		if end == nil {
			if _, err := collector.Finish(); err == nil {
				t.Fatal("control-only EOF succeeded")
			}
		} else {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := compiled.DecodeFrame(ctx, control, options); err == nil {
				t.Fatal("cancel ignored")
			}
		}
	}
}

func TestResponsesTurnStatePolicyAndInvalidBoundaries(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, raw := range []string{
		`{"type":"response.metadata","headers":null}`,
		`{"type":"response.metadata","headers":{}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":null}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":123}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":""}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s\nt"}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s\u007ft"}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s","X-Codex-Turn-State":"t"}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s","x-codex-turn-state":"t"}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s","other":"t"}}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s"},"text":"body"}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s"},"sequence_number":null}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s"},"sequence_number":-1}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s"},"sequence_number":1.1}`,
		`{"type":"response.metadata","headers":{"x-codex-turn-state":"s"},"sequence_number":"1"}`,
	} {
		if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
			t.Fatal("invalid control accepted", raw)
		}
		if err := from.ValidateWireOutput(p.EncodeEvent, testValue(t, raw)); err == nil {
			t.Fatal("invalid after mapping accepted", raw)
		}
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	control := testValue(t, `{"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic-secret"},"sequence_number":9007199254740993}`)
	frame, err := from.DecodeFrame(t.Context(), control, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := from.DecodeFrame(t.Context(), control, options); err == nil {
		t.Fatal("repeated sequence accepted")
	}
	for _, codec := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			policy := p.DefaultConversionPolicy(to, from)
			if mode == "strict" {
				policy.Mode = mode
			}
			if mode == "disabled" {
				for i := range policy.Rules {
					if policy.Rules[i].Action == "responses_turn_state" {
						policy.Rules[i].Enabled = false
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
				if err == nil {
					t.Fatal("strict omission accepted")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := to.EncodeFrames(t.Context(), event, p.EvaluationContext{State: p.NewEvaluationState()})
			if codec != Responses && mode == "disabled" {
				if err == nil {
					t.Fatal("disabled omission accepted")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if codec == Responses {
				if len(out) != 1 || out[0] != control || len(sink.Issues()) != 0 {
					t.Fatal("same-wire state changed")
				}
			} else {
				if len(out) != 0 || event.ResponsesTurnState != nil || len(sink.Issues()) != 1 {
					t.Fatal("projection emitted control data")
				}
				issue := sink.Issues()[0]
				if issue.Code != p.ConversionDegraded || issue.Fidelity != "lossy_compatible" || issue.Path != p.ResponsesTurnStatePath || issue.RuleID != "responses-turn-state" || issue.PolicyHash != conversion.Hash || issue.Protocol != from.Identity() || issue.Direction != p.DecodeEvent || !strings.Contains(issue.Reason, "frame 1") || strings.Contains(issue.Reason, "synthetic-secret") {
					t.Fatalf("incomplete diagnostic: %+v", issue)
				}
			}
		}
	}
}

func TestResponsesTurnStateCustomModulesAndProtectedScope(t *testing.T) {
	definition := shippedProjectionProtocol(t, Responses).Definition()
	definition.ID = "old-responses-control-copy"
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	compile := func(d p.Definition) *p.Compiled {
		t.Helper()
		raw, _ := p.EncodeValue(d)
		c, issues := compiler.Compile(raw.Bytes())
		if c == nil {
			t.Fatal(issues)
		}
		return c
	}
	source := compile(definition)
	frame, err := source.DecodeFrame(t.Context(), testValue(t, syntheticTurnStateFrame), p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "source"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		target := shippedProjectionProtocol(t, codec)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := conversion.Event(t.Context(), frame.Events[0], conversion.VerificationRoute(source.Identity(), target.Identity(), p.SSE), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.EncodeFrames(t.Context(), projected, p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "source"}}); err != nil {
			t.Fatal(codec, err)
		}
	}
	if _, err := source.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "other"}}); err == nil {
		t.Fatal("state crossed account scope")
	}
	definition.Directions[p.EncodeEvent] = p.Mapping{Transform: &p.Expression{Op: "read"}}
	handwritten := compile(definition)
	for _, rule := range p.DefaultConversionPolicy(handwritten, source).Rules {
		if rule.Action == "responses_turn_state" {
			t.Fatal("guessed handwritten target")
		}
	}
	// A mixed definition is selected by the actual decode direction.
	mixed := source.Definition()
	mixed.Directions[p.EncodeEvent] = shippedProjectionProtocol(t, Chat).Definition().Directions[p.EncodeEvent]
	mixedSource := compile(mixed)
	found := false
	for _, rule := range p.DefaultConversionPolicy(shippedProjectionProtocol(t, Chat), mixedSource).Rules {
		found = found || rule.Action == "responses_turn_state"
	}
	if !found {
		t.Fatal("mixed source lost decoder-based adaptation")
	}
	oversized := testValue(t, `{"type":"response.metadata","headers":{"x-codex-turn-state":"`+strings.Repeat("s", p.DefaultLimits().BufferBytes)+`"}}`)
	if _, err := source.DecodeFrame(t.Context(), oversized, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
		t.Fatal("oversized state accepted")
	}
	rule := p.ConversionRule{ID: "forge-state", Enabled: true, Phase: p.ConversionEvent, Action: "set", Path: "/responsesTurnState/frame/headers/x-codex-turn-state", Value: p.StringValue("forged")}
	c, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: 1, ID: "forge", Rules: []p.ConversionRule{rule}})
	if err == nil {
		_, err = c.Event(t.Context(), frame.Events[0], p.ConversionContext{}, nil)
	}
	if err == nil {
		t.Fatal("authored mutation minted opaque state")
	}
}
