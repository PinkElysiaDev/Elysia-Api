package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func deliveryRangeEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	document.Spec.Documents["client-event"].Stream.Events["observation"] = ProtocolStreamEventSpec{{Action: "observe"}}
	entry := document.Mapping.Directions[EncodeEvent]
	empty := literalExpression([]any{})
	texts, ranges := readExpression("input", "/texts"), readExpression("input", "/ranges")
	entry.DeliveryRanges = []ProtocolDeliveryRange{{Name: "custom-text",
		Measure: Expression{Op: "utf16_length", Source: expressionPointer(readExpression("input", ""))},
		Texts:   Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &texts}), Then: &texts, Otherwise: &empty},
		Ranges:  Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &ranges}), Then: &ranges, Otherwise: &empty},
	}}
	document.Mapping.Directions[EncodeEvent] = entry
	return document
}

func deliveryObservation(t *testing.T, sequence int, texts, ranges string) Value {
	t.Helper()
	fields := map[string]any{"kind": "observation", "response_id": "r", "seq": sequence}
	if texts != "" {
		fields["texts"] = parseTestValue(t, texts)
	}
	if ranges != "" {
		fields["ranges"] = parseTestValue(t, ranges)
	}
	return fixtureValue(t, fields)
}

func startDeliveryRangeStream(t *testing.T, compiled *Compiled) *WireStreamValidation {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validation.Release)
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
		t.Fatal(err)
	}
	if err := validation.CommitDelivery(); err != nil {
		t.Fatal(err)
	}
	return validation
}

func TestDeliveryStreamRequiresFinalLifecycle(t *testing.T) {
	document := deliveryRangeEventPackage(t)
	contract := document.Spec.Documents["client-event"]
	contract.Stream = nil
	document.Spec.Documents["client-event"] = contract
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	_, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err == nil || !strings.Contains(err.Error(), "deliveryRanges") {
		t.Fatal("delivery declarations accepted an unconnected lifecycle", issues)
	}
}

func TestDeliveryStreamLateRanges(t *testing.T) {
	compiled := compilePackageForTest(t, deliveryRangeEventPackage(t))
	for _, test := range []struct {
		name, original, final string
		valid                 bool
	}{
		{"unchanged-prefix", `[0,4]`, `[0,4]`, true},
		{"adjusted-suffix", `[6,8]`, `[5,7]`, true},
		{"old-indices", `[6,8]`, `[6,8]`, false},
		{"rewritten-middle", `[4,6]`, `[4,5]`, false},
		{"cross-rewrite", `[3,7]`, `[3,6]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			validation := startDeliveryRangeStream(t, compiled)
			before := deliveryObservation(t, 1, `[{"key":"t","text":"😀abCDxy","mode":"append","path":"/body"}]`, "")
			after := deliveryObservation(t, 1, `[{"key":"t","text":"😀abQxy","mode":"append","path":"/body"}]`, "")
			if err := validation.ConsumeTransformed(t.Context(), before, after, EvaluationContext{}); err != nil {
				t.Fatal("unreferenced text edit was rejected", err)
			}
			if len(validation.deliveryRanges.texts) != 0 || validation.packageStream.delivered.sequence != 0 {
				t.Fatal("accepted text was published before delivery")
			}
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
			makeRange := func(bounds string) string {
				var coordinates []int
				if err := parseTestValue(t, bounds).Decode(&coordinates); err != nil {
					t.Fatal(err)
				}
				return fixtureValue(t, []any{map[string]any{"key": "t", "id": "citation", "start": coordinates[0], "end": coordinates[1], "path": "/citation"}}).raw
			}
			before = deliveryObservation(t, 2, "", makeRange(test.original))
			after = deliveryObservation(t, 2, "", makeRange(test.final))
			err := validation.ConsumeTransformed(t.Context(), before, after, EvaluationContext{})
			if (err == nil) != test.valid {
				t.Fatal("unexpected late range result", err)
			}
			if !test.valid {
				if validation.CommitDelivery() == nil || validation.Finish() == nil {
					t.Fatal("rejected reference left a resumable output")
				}
				return
			}
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
			items := validation.deliveryRanges.items
			before = deliveryObservation(t, 3, "", makeRange(test.original))
			after = deliveryObservation(t, 3, "", makeRange(test.final))
			if err := validation.ConsumeTransformed(t.Context(), before, after, EvaluationContext{}); err != nil {
				t.Fatal("repeated reference changed its proof", err)
			}
			if validation.deliveryRanges.items != items {
				t.Fatal("repeated reference accumulated identities")
			}
		})
	}
}

func TestDeliveryStreamSnapshotAndAbort(t *testing.T) {
	compiled := compilePackageForTest(t, deliveryRangeEventPackage(t))
	for _, rejectPrefix := range []bool{false, true} {
		validation := startDeliveryRangeStream(t, compiled)
		first := deliveryObservation(t, 1, `[{"key":"t","text":"😀a","mode":"append","path":"/body"}]`, "")
		if err := validation.Consume(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		if err := validation.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
		before := deliveryObservation(t, 2, `[{"key":"t","text":"😀ab","mode":"snapshot","path":"/body"}]`, "")
		after := before
		if rejectPrefix {
			after = deliveryObservation(t, 2, `[{"key":"t","text":"😁ab","mode":"snapshot","path":"/body"}]`, "")
		}
		err := validation.ConsumeTransformed(t.Context(), before, after, EvaluationContext{})
		if (err != nil) != rejectPrefix {
			t.Fatal("snapshot prefix proof", err)
		}
		state := validation.deliveryRanges.texts[deliveryRangeKey{0, "t"}]
		if !rejectPrefix && (state.positions.source != 3 || state.source.Length() != len("😀a")) {
			t.Fatal("snapshot committed before delivery", state)
		}
		validation.AbortDelivery()
		if len(validation.deliveryRanges.texts) != 0 || len(validation.deliveryRanges.pending) != 0 || validation.deliveryRanges.stagedBytes != 0 || validation.packageStream.delivered.sequence != 1 || validation.CommitDelivery() == nil || validation.Consume(t.Context(), before) == nil {
			t.Fatal("failed delivery retained or resumed its pending text")
		}
		validation.Release()
		if validation.deliveryRanges != nil || validation.packageStream != nil || validation.Consume(t.Context(), before) == nil {
			t.Fatal("released validation retained or resumed its stream")
		}
	}
}

func TestDeliveryStreamIntervalStorage(t *testing.T) {
	measure := func(text string) (int64, error) { return int64(utf16TextLength(text)), nil }
	stream := &deliveryRangeStream{}
	key := deliveryRangeKey{0, "t"}
	appendText := func(before, after string) {
		t.Helper()
		pending := stream.edit(key)
		if _, err := pending.observe(deliveryTextObservation{text: before, mode: "append"}, deliveryTextObservation{text: after, mode: "append"}, measure); err != nil {
			t.Fatal(err)
		}
		if err := stream.commit(); err != nil {
			t.Fatal(err)
		}
	}
	for range 1024 {
		appendText("😀a", "😀a")
	}
	previous := stream.texts[key]
	if len(previous.positions.spans) != 0 || previous.source != previous.output || previous.positions.source != 3072 || stream.items != 1 {
		t.Fatal("unchanged increments accumulated explicit spans or duplicate digests")
	}
	for range 32 {
		appendText("abc", "axc")
	}
	spans := append([]streamPositionSpan(nil), previous.positions.spans...)
	pending := stream.edit(key)
	if pending.positions.base != len(spans)-1 || len(pending.positions.next.spans) != 1 {
		t.Fatal("transaction copied the complete interval history")
	}
	if _, err := pending.observe(deliveryTextObservation{text: "abc", mode: "append"}, deliveryTextObservation{text: "axc", mode: "append"}, measure); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(previous.positions.spans, spans) {
		t.Fatal("staged tail changed committed intervals")
	}
	if _, _, err := pending.positions.project(0, 3); err != nil {
		t.Fatal("immutable prefix was lost", err)
	}
}

func TestDeliveryStreamResourceBudgets(t *testing.T) {
	for _, limit := range []string{"items", "bytes"} {
		compiled := compilePackageForTest(t, deliveryRangeEventPackage(t))
		if limit == "items" {
			compiled.limits.StateItems = 1
		} else {
			compiled.limits.BufferBytes = 256
		}
		validation := startDeliveryRangeStream(t, compiled)
		var err error
		for sequence := 1; sequence <= 50 && err == nil; sequence++ {
			before := deliveryObservation(t, sequence, `[{"key":"t","text":"abc","mode":"append","path":"/body"}]`, "")
			after := deliveryObservation(t, sequence, `[{"key":"t","text":"axc","mode":"append","path":"/body"}]`, "")
			err = validation.ConsumeTransformed(t.Context(), before, after, EvaluationContext{})
		}
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Metric != "state_"+limit || !validation.deliveryAborted {
			t.Fatal("delivery state escaped its budget", limit, err)
		}
	}
}

func TestDeliveryStreamCombinationAndWirePreview(t *testing.T) {
	compiled := compilePackageForTest(t, deliveryRangeEventPackage(t))
	wire := []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`),
		packageStreamFrame(ItemDelta, 2, `,"item_id":"t","text":"abc","texts":[{"key":"t","text":"abc","mode":"append","path":"/texts/0/text"}]`),
		packageStreamFrame(ItemFinished, 3, `,"item_id":"t","ranges":[{"key":"t","id":"citation","start":1,"end":2,"path":"/ranges/0"}]`),
		packageStreamFrame(ResponseFinished, 4, ""),
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	service := &Service{compiler: compiler}
	service.snapshot.Store(&registrySnapshot{entries: map[string]*Compiled{compiled.Identity().DefinitionID: compiled}})
	for _, text := range []string{"abc", "axc"} {
		present := true
		policy := ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "rewrite", Rules: []ConversionRule{{ID: "rewrite", Order: 1000, Enabled: true, Phase: ConversionWire, Action: "set", Path: "/texts/0/text", Value: StringValue(text), Match: ConversionMatch{Path: "/texts/0/text", Present: &present}}}}
		conversion, err := ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: SSE}
		options := EvaluationContext{State: NewEvaluationState()}
		var frames []*EventFrame
		for _, value := range wire {
			frame, err := compiled.DecodeFrame(t.Context(), value, options)
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, frame)
		}
		options.State.Release()
		options.State = NewEvaluationState()
		_, combinationErr := verifyCombinationEventOutput(t.Context(), compiled, frames, options, conversion, route)
		options.State.Release()
		result := service.PreviewWorkflow(t.Context(), PreviewInput{Mode: "conversion", Direction: EncodeEvent, ConversionPhase: ConversionWire, ConversionPolicy: &policy, ConversionContext: route, Input: fixtureValue(t, wire), Sequence: true})
		for _, err := range []error{combinationErr, IssuesError(result.Issues)} {
			if text == "abc" && err != nil || text != "abc" && (err == nil || !strings.Contains(err.Error(), "unproven text")) {
				t.Fatal("combination and final wire preview disagreed with delivery", text, err)
			}
		}
	}
}

func FuzzDeliveryStreamTransactions(f *testing.F) {
	f.Add("😀abCDxy", "😀abQxy", uint8(3))
	f.Add("abc", "axc", uint8(1))
	f.Add("same", "same", uint8(2))
	f.Fuzz(func(t *testing.T, before, after string, interval uint8) {
		if !utf8.ValidString(before) || !utf8.ValidString(after) || len(before)+len(after) > 128 {
			t.Skip()
		}
		stream := &deliveryRangeStream{}
		var oracle deliveryTextPositions
		for index := range 8 {
			pending := stream.edit(deliveryRangeKey{0, "text"})
			if _, err := pending.observe(deliveryTextObservation{text: before, mode: "append"}, deliveryTextObservation{text: after, mode: "append"}, measureDeliveryUTF16); err != nil {
				t.Fatal(err)
			}
			if err := oracle.append(before, after, measureDeliveryUTF16); err != nil {
				t.Fatal(err)
			}
			for start := int64(0); start < oracle.source; start++ {
				a, b, err := pending.positions.project(start, start+1)
				wantA, wantB, wantErr := oracle.project(start, start+1)
				if (err == nil) != (wantErr == nil) || err == nil && (a != wantA || b != wantB) {
					t.Fatal("transaction changed the interval proof", index, start, a, b, err, wantA, wantB, wantErr)
				}
			}
			if index%int(interval%4+1) == 0 {
				if err := stream.commit(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func verifyCombinationEventOutput(ctx context.Context, compiled *Compiled, frames []*EventFrame, options EvaluationContext, conversion *CompiledConversion, route ConversionContext) ([]Event, error) {
	return verifyCombinationEventStream(ctx, compiled, options, conversion, route, func(emit func(*EventFrame) error) error {
		for _, frame := range frames {
			if err := emit(frame); err != nil {
				return err
			}
		}
		return nil
	})
}
