package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const sourceOpening = `{"kind":"response.started","response_id":"pending","seq":0}`
const sourceFollowing = `{"kind":"item.started","response_id":"actual","item_id":"text","seq":1}`

func sourcePreludePackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	shape := ValueSchema{Type: ObjectType, AllowUnknown: true}
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{
		ID: "opening-identity", Order: 10, Enabled: true, Phase: ConversionSourceEvent,
		SourcePrelude: &ProtocolSourcePrelude{
			First: ProtocolSourceFrameMatch{Schema: shape}, Next: ProtocolSourceFrameMatch{Schema: shape},
			When: literalExpression(true), Reason: "bind an undeclared opening identity before decoding",
			Set: []ProtocolSourceUpdate{{Path: "/response_id", Value: readExpression("input", "/response_id")}},
		},
	}}
	return document
}

func sourcePreludeState(t *testing.T, document ProtocolPackage, mode string) (*SourceEventState, *StreamBudget) {
	t.Helper()
	source := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(source, source)
	policy.Mode = mode
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: source.Identity(), Target: Identity{DefinitionID: "independent-target"}, Transport: SSE}
	state := NewSourceEventState(conversion, route, source, &DiagnosticSink{})
	budget := NewStreamBudget(source.limits)
	if err := budget.Bind(state); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(state.Release)
	return state, budget
}

func TestSourcePreludeBindsSourceAndPreservesFrameEvidence(t *testing.T) {
	state, budget := sourcePreludeState(t, sourcePreludePackage(t), "compatible")
	metadata := Object{"event": StringValue("original")}
	first := Value{raw: sourceOpening}
	if frames, err := state.Push(t.Context(), first, metadata); err != nil || len(frames) != 0 || budget.items != 1 {
		t.Fatal("opening was not retained once", frames, budget.items, err)
	}
	metadata["event"] = StringValue("mutated")
	frames, err := state.Push(t.Context(), Value{raw: sourceFollowing}, nil)
	if err != nil || len(frames) != 2 || budget.items != 0 || budget.bytes != 0 {
		t.Fatal("pair was not released in source order", frames, budget.items, err)
	}
	if frames[0].Ordinal != 1 || frames[1].Ordinal != 2 || frames[0].Metadata["event"] != StringValue("original") || first.raw != sourceOpening || frames[1].Value.raw != sourceFollowing {
		t.Fatal("normalization changed independent evidence", frames)
	}
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, frame := range frames {
		decoded, err := state.source.DecodeFrame(t.Context(), frame.Value, options)
		if err != nil || len(decoded.Events) != 1 || decoded.Events[0].ResponseID != StringValue("actual") {
			t.Fatal("source lifecycle did not consume normalized evidence", decoded, err)
		}
	}
	changed := Value{raw: `{"kind":"item.delta","response_id":"later","item_id":"text","seq":2,"text":"x"}`}
	frames, err = state.Push(t.Context(), changed, nil)
	if err != nil || len(frames) != 1 || frames[0].Value != changed {
		t.Fatal("normalizer rewrote later frames", frames, err)
	}
	if _, err := state.source.DecodeFrame(t.Context(), frames[0].Value, options); err == nil {
		t.Fatal("source identity guard was bypassed")
	}
	issue := state.sink.Issues()[0]
	if issue.Protocol != state.source.identity || issue.Direction != DecodeEvent || issue.Stage != "conversion.source_event" || issue.PolicyHash != state.conversion.Hash {
		t.Fatal("source provenance lost", issue)
	}
}

func TestSourcePreludeCannotHideInvalidSourceFrames(t *testing.T) {
	for _, fixture := range []struct{ name, first, next string }{
		{"invalid-first", `{"kind":"response.started","response_id":false,"seq":0}`, sourceFollowing},
		{"invalid-next", sourceOpening, `{"kind":"item.started","response_id":"actual","item_id":"text"}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			state, budget := sourcePreludeState(t, sourcePreludePackage(t), "compatible")
			_, err := state.Push(t.Context(), Value{raw: fixture.first}, nil)
			if err == nil {
				_, err = state.Push(t.Context(), Value{raw: fixture.next}, nil)
			}
			if err == nil || state.pending != nil || budget.items != 0 || len(state.sink.Issues()) != 0 {
				t.Fatal("invalid raw input was repaired or retained", err, state.sink.Issues())
			}
		})
	}
	document := sourcePreludePackage(t)
	document.Mapping.ConversionDefaults[0].SourcePrelude.Set[0].Value = literalExpression(false)
	state, budget := sourcePreludeState(t, document, "compatible")
	_, _ = state.Push(t.Context(), Value{raw: sourceOpening}, nil)
	if frames, err := state.Push(t.Context(), Value{raw: sourceFollowing}, nil); err == nil || len(frames) != 0 || budget.items != 0 || len(state.sink.Issues()) != 0 {
		t.Fatal("invalid normalized frame was published", frames, err)
	}
}

func TestSourcePreludeRespectsEffectivePolicyAndRevision(t *testing.T) {
	for _, mode := range []string{"strict", "disabled", "replaced", "route", "decoder"} {
		t.Run(mode, func(t *testing.T) {
			document := sourcePreludePackage(t)
			state, _ := sourcePreludeState(t, document, "compatible")
			policy := state.conversion.Policy
			switch mode {
			case "strict":
				policy.Mode = "strict"
			case "disabled":
				policy.Rules[0].Enabled = false
			case "replaced":
				options := state.conversion.options[policy.Rules[0].ID].value.(sourcePreludeOptions)
				options.Prelude.When = literalExpression(false)
				policy.Rules[0].Value, _ = EncodeValue(options)
			case "route":
				state.route.Source.Revision = "different-revision"
			case "decoder":
				document.Mapping.Directions[DecodeEvent].Program.Fields["delta"] = literalExpression("different decoder")
				state.source = compilePackageForTest(t, document)
			}
			var err error
			state.conversion, err = ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			state.isClosed = len(state.conversion.sourcePreludes) == 0
			output, err := state.Sequence(t.Context(), Value{raw: "[" + sourceOpening + "," + sourceFollowing + "]"})
			if mode == "disabled" || mode == "replaced" {
				if err != nil || !strings.Contains(output.raw, "pending") || len(state.sink.Issues()) != 0 {
					t.Fatal("ineffective declaration changed input", output, err)
				}
			} else if err == nil || !output.IsZero() || len(state.sink.Issues()) != 0 {
				t.Fatal("strict or mismatched source binding was accepted", output, err)
			}
		})
	}
}

func TestSourcePreludeCleanupAndAggregateBudget(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled, errors.New("transport failure")} {
		state, budget := sourcePreludeState(t, sourcePreludePackage(t), "compatible")
		_, _ = state.Push(t.Context(), Value{raw: sourceOpening}, nil)
		frames := state.Finish(failure)
		if (len(frames) == 1) != (failure == nil) || budget.items != 0 || budget.bytes != 0 || len(state.Finish(nil)) != 0 {
			t.Fatal("finish lost original input or retained state", frames, budget)
		}
	}
	state, budget := sourcePreludeState(t, sourcePreludePackage(t), "compatible")
	budget.limits.BufferBytes = len(sourceOpening)
	if _, err := state.Push(t.Context(), Value{raw: sourceOpening}, Object{"event": StringValue("metadata")}); err == nil || budget.items != 0 || state.pending != nil {
		t.Fatal("aggregate ignored retained transport metadata", err)
	}
	state, budget = sourcePreludeState(t, sourcePreludePackage(t), "compatible")
	_, _ = state.Push(t.Context(), Value{raw: sourceOpening}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := state.Push(ctx, Value{raw: sourceFollowing}, nil); !errors.Is(err, context.Canceled) || budget.items != 0 {
		t.Fatal("cancellation retained the opening", err)
	}
	state.Release()
	if err := budget.Bind(state); err == nil {
		t.Fatal("released normalizer rebound")
	}
}

func TestSourcePreludeRejectsAmbiguousOrAuthoritativeDeclarations(t *testing.T) {
	for _, variant := range []string{"overlap", "root", "runtime", "steps", "phase", "missing-update", "non-object", "no-decoder"} {
		t.Run(variant, func(t *testing.T) {
			document := sourcePreludePackage(t)
			declaration := &document.Mapping.ConversionDefaults[0]
			switch variant {
			case "overlap":
				declaration.SourcePrelude.Set = append(declaration.SourcePrelude.Set, ProtocolSourceUpdate{Path: "/response_id/child", Value: literalExpression("x")})
			case "root":
				declaration.SourcePrelude.Set[0].Path = ""
			case "runtime":
				declaration.SourcePrelude.Set[0].Value = readExpression("runtime", "/nativeFrame")
			case "steps":
				declaration.Steps = []ProtocolControlStep{{}}
			case "phase":
				declaration.Phase = ConversionEvent
			case "missing-update":
				declaration.SourcePrelude.Set = nil
			case "non-object":
				declaration.SourcePrelude.First.Schema.Type = ArrayType
			case "no-decoder":
				delete(document.Mapping.Directions, DecodeEvent)
			}
			compiler, _ := NewCompiler(DefaultLimits(), nil)
			raw, _ := EncodeValue(document)
			if _, issues := compiler.CompilePackage(raw.Bytes()); IssuesError(issues) == nil {
				t.Fatal("invalid prelude declaration compiled", variant)
			}
		})
	}
}

func TestSourcePreludeCombinationUsesTheSameSourceBoundary(t *testing.T) {
	for _, variant := range []string{"compatible", "strict", "disabled", "invalid-source", "invalid-output"} {
		t.Run(variant, func(t *testing.T) {
			source := compilePackageForTest(t, sourcePreludePackage(t))
			document := eventProtocolPackage(t)
			document.Spec.ID = "independent-target"
			document.Mapping.Native.Preserve = false
			if variant == "invalid-output" {
				document.Mapping.Directions[EncodeEvent].Program.Fields["seq"] = literalExpression(-1)
			}
			target := compilePackageForTest(t, document)
			policy := DefaultConversionPolicy(target, source)
			if variant == "strict" {
				policy.Mode = "strict"
			} else if variant == "disabled" {
				policy.Rules[0].Enabled = false
			}
			conversion, err := ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			first := sourceOpening
			if variant == "invalid-source" {
				first = strings.Replace(first, `"seq":0`, `"seq":null`, 1)
			}
			input := Value{raw: "[" + first + "," + sourceFollowing + `,{"kind":"item.finished","response_id":"actual","item_id":"text","seq":2},{"kind":"response.finished","response_id":"actual","seq":3}]`}
			sample := Sample{ID: "opening-pair", Direction: DecodeEvent, Input: input, Sequence: true}
			sink := &DiagnosticSink{}
			ctx := context.WithValue(t.Context(), conversionVerificationKey{}, conversion)
			ctx = context.WithValue(ctx, conversionVerificationDiagnostics{}, sink)
			err = verifyEventCombination(ctx, target, source, sample)
			if variant == "compatible" {
				if err != nil || len(sink.Issues()) != 1 || sink.Issues()[0].Stage != "conversion.source_event" {
					t.Fatal("combination skipped the declared source boundary", err, sink.Issues())
				}
			} else if err == nil {
				t.Fatal("combination hid an invalid or unauthorized stream", variant)
			}
		})
	}
}
