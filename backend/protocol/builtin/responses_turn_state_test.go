package builtin

import (
	"context"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const syntheticTurnStateFrame = `{"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic-turn-state"}}`

func TestResponsesTurnStateCustomModulesAndProtectedScope(t *testing.T) {
	document, _ := testCompiled(t, Responses).Package()
	document.Spec.ID = "custom-responses-control"
	source, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "source"}})
	defer options.State.Release()
	frame, err := source.DecodeFrame(t.Context(), testValue(t, syntheticTurnStateFrame), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		target := testCompiled(t, codec)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
		if err != nil {
			t.Fatal(err)
		}
		route := conversion.VerificationRoute(source.Identity(), target.Identity(), p.SSE)
		route.Scope = options.Scope
		projected, err := conversion.Event(t.Context(), frame.Events[0], route, nil)
		if err != nil {
			t.Fatal(err)
		}
		output := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: options.Scope})
		defer output.State.Release()
		output, consume := bindFixtureOutput(t, target, output)
		frames, err := target.EncodeFrames(t.Context(), projected, output)
		if err != nil {
			t.Fatal(codec, err)
		}
		consume(frames)
	}
	other := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "other"}})
	defer other.State.Release()
	if _, err := source.EncodeFrame(t.Context(), frame, other); err == nil {
		t.Fatal("state crossed account scope")
	}
	oversized := testValue(t, `{"type":"response.metadata","headers":{"x-codex-turn-state":"`+strings.Repeat("s", p.DefaultLimits().BufferBytes)+`"}}`)
	if _, err := source.DecodeFrame(t.Context(), oversized, options); err == nil {
		t.Fatal("oversized state accepted")
	}
	for _, isMixed := range []bool{false, true} {
		authored, _ := source.Package()
		mapping := authored.Mapping.Directions[p.EncodeEvent]
		if isMixed {
			other, _ := testCompiled(t, Chat).Package()
			mapping = other.Mapping.Directions[p.EncodeEvent]
		} else {
			mapping.Program = p.Expression{Op: "read"}
		}
		authored.Mapping.Directions[p.EncodeEvent] = mapping
		if compiled, issues := compilePackageDocument(t, authored); compiled != nil || p.IssuesError(issues) == nil {
			t.Fatal("unproved or foreign stream program bypassed its own Spec/Mapping contract", isMixed)
		}
	}
	for _, path := range []string{"/responsesTurnState/frame/headers/x-codex-turn-state", "/control/value"} {
		rule := p.ConversionRule{ID: "forge-state", Enabled: true, Phase: p.ConversionEvent, Action: "set", Path: path, Value: p.StringValue("forged")}
		conversion, err := p.ResolveConversion(p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "forge", Rules: []p.ConversionRule{rule}})
		if err == nil {
			_, err = conversion.Event(t.Context(), frame.Events[0], p.ConversionContext{}, nil)
		}
		if err == nil {
			t.Fatal("authored mutation minted opaque state", path)
		}
	}
}

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
	for _, candidate := range packageMapping(compiled).Samples {
		if candidate.ID == "declared-stream-text-decode" {
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
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Account: "a"}})
	defer options.State.Release()
	collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, options.Scope), compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	validator, _ := compiled.NewWireStreamValidation(options.Scope)
	defer validator.Release()
	options = options.WithOutputStream(validator)
	for _, input := range frames {
		decoded, err := compiled.DecodeFrame(t.Context(), input, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range decoded.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
			if event.Control != nil && (!event.ResponseID.IsZero() || event.Usage != nil || len(event.Metadata) != 0) {
				t.Fatal("control state entered generation")
			}
		}
		output, err := compiled.EncodeFrame(t.Context(), decoded, options)
		if err != nil || len(output) != 1 || output[0] != input {
			semantic, _ := p.EncodeValue(decoded.Events)
			t.Fatalf("native frame changed: %v; %s", err, semantic.JSONString())
		}
		if err := validator.Consume(t.Context(), output[0]); err != nil {
			t.Fatal(err)
		}
		if err := validator.CommitDelivery(); err != nil {
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
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		frame, err := compiled.DecodeFrame(t.Context(), control, options)
		if err != nil {
			t.Fatal(err)
		}
		collector, _ := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), compiled.ResourceLimits())
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
		if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})); err == nil {
			t.Fatal("invalid control accepted", raw)
		}
		validation, err := from.NewWireStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		err = validation.Consume(t.Context(), testValue(t, raw))
		validation.Release()
		if err == nil {
			t.Fatal("invalid after mapping accepted", raw)
		}
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
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
					if policy.Rules[i].ID == "event-control" {
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
			out, err := to.EncodeFrames(t.Context(), event, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
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
				if len(out) != 1 || len(sink.Issues()) != 0 {
					t.Fatal("same-wire state changed")
				}
				sameJSON(t, out[0].Bytes(), control.JSONString())
			} else {
				if len(out) != 0 || event.Control != nil || len(sink.Issues()) != 1 {
					t.Fatal("projection emitted control data", codec, mode, out, event.Control, sink.Issues())
				}
				issue := sink.Issues()[0]
				if issue.Code != p.ConversionDegraded || issue.Fidelity != "lossy_compatible" || issue.Path != "/headers" || issue.RuleID != "event-control" || issue.PolicyHash != conversion.Hash || issue.Protocol != from.Identity() || issue.Direction != p.DecodeEvent || !strings.Contains(issue.Reason, "frame 1") || strings.Contains(issue.Reason, "synthetic-secret") {
					t.Fatalf("incomplete diagnostic: %+v", issue)
				}
			}
		}
	}
}
