package protocol

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestPackageEventPreviewSharesStreamBudget(t *testing.T) {
	wire := []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(ItemStarted, 1, `,"item_id":"t"`),
		packageStreamFrame(ItemDelta, 2, `,"item_id":"t","text":"hi"`),
		packageStreamFrame(ItemFinished, 3, `,"item_id":"t"`),
		packageStreamFrame(ResponseFinished, 4, ""),
	}
	events := []Event{
		{Type: ResponseStarted},
		{Type: ItemStarted, ItemID: StringValue("t"), Item: &Node{Kind: TextNode}},
		{Type: ItemDelta, ItemID: StringValue("t"), Delta: StringValue("hi")},
		{Type: ItemFinished, ItemID: StringValue("t")},
		{Type: ResponseFinished},
	}
	for index := range events {
		events[index].SchemaVersion = ProtocolCoreVersion
		events[index].ResponseID = StringValue("r")
		events[index].Sequence = coreInteger(int64(index))
	}
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		for _, capacity := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", direction, capacity), func(t *testing.T) {
				document := eventProtocolPackage(t)
				limits := DefaultLimits()
				limits.StateItems = capacity
				document.Mapping.Limits = &limits
				compiled := compilePackageForTest(t, document)
				input := fixtureValue(t, wire)
				if direction == EncodeEvent {
					input = fixtureValue(t, events)
				}
				options := newVerificationContext(Scope{}, nil)
				defer options.State.Release()
				result, err := executeEventFixtureWithState(t.Context(), compiled, Sample{Direction: direction, Sequence: true, Input: input}, options)
				if capacity == 1 {
					var failure *ResourceLimitError
					if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 2 || !result.output.IsZero() {
						t.Fatal("fixture gave wire and semantic state separate budgets", err)
					}
				} else if err != nil || len(result.semantic.([]Event)) != len(events) {
					t.Fatal("bounded fixture lost its complete stream", err)
				}
				budget := options.State.retention.budget
				if len(budget.sequences) != 0 || budget.items != options.State.packageStateItems || budget.bytes != options.State.packageStateBytes {
					t.Fatal("fixture retained wire or replay state after return")
				}
				compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
				service := &Service{compiler: compiler}
				preview := service.Preview(t.Context(), fixtureValue(t, document).Bytes(), direction, input, true, EvaluationContext{})
				if capacity == 1 && !hasIssueCode(IssuesError(preview.Issues), LimitExceeded) || capacity == 2 && IssuesError(preview.Issues) != nil {
					t.Fatal("editor preview diverged from fixture retention", preview.Issues)
				}
			})
		}
	}
}

func TestPackageEventVerificationChecksWireLifecycle(t *testing.T) {
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		for _, fault := range []string{"sequence", "EOF"} {
			t.Run(string(direction)+"/"+fault, func(t *testing.T) {
				document := eventProtocolPackage(t)
				mapping := document.Mapping.Directions[direction]
				code, path := UpstreamContractViolation, "/seq"
				var inputs []Value
				if direction == DecodeEvent {
					inputs = []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ResponseFinished, 0, "")}
					// The semantic replay cannot see a bad wire sequence omitted by
					// the mapping, or a missing terminal replaced with a literal.
					mapping.Program.Fields["sequence"] = Expression{Op: "omit"}
					if fault == "EOF" {
						inputs = inputs[:1]
						mapping.Program.Fields["type"] = literalExpression(ResponseFinished)
					}
				} else {
					code = ConversionRejected
					inputs = []Value{
						fixtureValue(t, Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 0)}),
						fixtureValue(t, Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, ResponseID: StringValue("r"), Sequence: fixtureValue(t, 1)}),
					}
					if fault == "sequence" {
						mapping.Program.Fields["seq"] = literalExpression(0)
					} else {
						isTerminal := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression(ResponseFinished)}}
						mapping.Program.Fields["kind"] = Expression{Op: "if", When: &isTerminal, Then: expressionPointer(literalExpression(UsageUpdated)), Otherwise: expressionPointer(readExpression("input", "/type"))}
						mapping.Program.Fields["usage"] = objectExpression(map[string]Expression{"input": literalExpression(1)})
					}
				}
				if fault == "EOF" {
					path = "/kind"
				}
				document.Mapping.Directions[direction] = mapping
				sample := Sample{ID: "wire-lifecycle", Direction: direction, Sequence: true, Input: fixtureValue(t, inputs), ExpectedIssue: code}
				document.Mapping.Samples = []Sample{sample}
				compiled := compilePackageForTest(t, document)
				_, err := executeEventFixture(t.Context(), compiled, sample)
				assertPackageRuntimeIssue(t, err, code, path)
				report := Verify(t.Context(), compiled)
				if len(report.Checks) != 1 || !report.Checks[0].Passed {
					t.Fatal("offline verification bypassed the independent wire lifecycle", report.Checks, report.Issues)
				}
			})
		}
	}
}

func TestPackageEventCombinationUsesFinalDeliveredPosition(t *testing.T) {
	source := eventProtocolPackage(t)
	source.Spec.ID = "position-source"
	source.Mapping.Native.Preserve = false
	target := outputPositionPackage(t)
	target.Mapping.Native.Preserve = false
	delete(target.Mapping.Directions[EncodeEvent].Program.Fields, "observed")
	isStart := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression(ResponseStarted)}}
	nextSequence := Expression{Op: "integer", Operation: "add", Items: []Expression{
		readExpression("runtime", "/stream/sequence"), literalExpression(1),
	}}
	target.Mapping.Directions[EncodeEvent].Program.Fields["seq"] = Expression{
		Op: "if", When: &isStart, Then: expressionPointer(readExpression("input", "/sequence")), Otherwise: &nextSequence,
	}
	ingress, upstream := compilePackageForTest(t, target), compilePackageForTest(t, source)
	sample := Sample{Direction: DecodeEvent, Sequence: true, Input: fixtureValue(t, []Value{
		packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(ResponseFinished, 1, ""),
	})}
	for _, hasInvalidTerminal := range []bool{false, true} {
		name := "follows-final-wire"
		if hasInvalidTerminal {
			name = "rejects-final-wire"
		}
		t.Run(name, func(t *testing.T) {
			condition := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(ResponseStarted)}}
			if hasInvalidTerminal {
				condition = literalExpression(true)
			}
			// Move only the start to 41. The target must observe that final wire
			// and generate 42, not the source's 1 or the pre-edit target's 1.
			replacement := Expression{Op: "merge", Policy: "reject", Items: []Expression{
				{Op: "project", Source: expressionPointer(readExpression("input", "")), Keys: []string{"seq"}, Policy: "exclude"},
				objectExpression(map[string]Expression{"seq": literalExpression(41)}),
			}}
			program := Expression{Op: "if", When: &condition, Then: &replacement, Otherwise: expressionPointer(readExpression("input", ""))}
			conversion, err := ResolveConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "final-position", Mode: "compatible", Rules: []ConversionRule{{
				ID: "edit-sequence", Order: 1, Enabled: true, Phase: ConversionWire, Action: "transform", Expression: &program,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), conversionVerificationKey{}, conversion)
			err = verifyEventCombination(ctx, ingress, upstream, sample)
			if hasInvalidTerminal {
				assertPackageRuntimeIssue(t, err, ConversionRejected, "/seq")
			} else if err != nil {
				t.Fatal("combination did not simulate delivery of the final wire batch", err)
			}
		})
	}
}
