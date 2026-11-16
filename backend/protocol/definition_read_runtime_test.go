package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type compactReadOutcome struct {
	value, reason, path  string
	steps, contextChecks int
	isCanceled           bool
	limit                *ResourceLimitError
}

func evaluateCompactFixture(t *testing.T, plan *compiledOutputPlan, input Value, limits Limits, stepLimit, cancelAt int, isPlanned bool) compactReadOutcome {
	t.Helper()
	ctx := &outputPlanCancelContext{Context: t.Context(), remaining: cancelAt}
	state := evaluation{ctx: ctx, input: input, root: input, item: input, context: input,
		bindings: []JSONOutput{JSONLeaf(input)}, streamSlots: []Value{input},
		runtime: evaluationRuntime{isBound: true, scopeModel: "model"},
		budget:  &evaluationBudget{limits: limits, stepLimit: stepLimit}}
	var value Value
	var err error
	if isPlanned {
		state.objectReader = plan.newRootObjectReader(input)
		value, err = plan.evaluate(state)
	} else {
		value, err = plan.expression.evaluate(state)
	}
	outcome := compactReadOutcome{value: value.JSONString(), reason: fmt.Sprint(err), path: mappingErrorPath("", err),
		steps: state.budget.steps, contextChecks: cancelAt - ctx.remaining, isCanceled: errors.Is(err, context.Canceled)}
	errors.As(err, &outcome.limit)
	return outcome
}

func TestCompactReadRuntimeParity(t *testing.T) {
	scope := expressionScope{isIteration: true, canReadRuntime: true, bindingDepth: 1,
		bindings:   map[string]expressionBinding{"name/with~escape": {slot: 0, result: AnyType}},
		stateSlots: map[string]expressionBinding{"slot": {slot: 0, result: AnyType}}}
	for _, fixture := range []struct {
		name, object, compact string
	}{
		{"input", `{"op":"read","path":"/a/x"}`, `"$input/a/x"`},
		{"root", `{"op":"read","from":"root","path":"/a/x"}`, `"$root/a/x"`},
		{"item", `{"op":"read","from":"item","path":"/a/x"}`, `"$item/a/x"`},
		{"context", `{"op":"read","from":"context","path":"/a/x"}`, `"$context/a/x"`},
		{"binding", `{"op":"read","from":"binding","name":"name/with~escape","path":"/a/x"}`, `"$binding/name~1with~0escape/a/x"`},
		{"state", `{"op":"read","from":"state","path":"/slot/a/x"}`, `"$state/slot/a/x"`},
		{"runtime", `{"op":"read","from":"runtime","path":"/scope/model"}`, `"$runtime/scope/model"`},
		{"escaped-pointer", `{"op":"read","path":"/a~1b/~0x"}`, `"$input/a~1b/~0x"`},
		{"empty-pointer", `{"op":"read"}`, `"$input"`},
		{"empty-key", `{"op":"read","path":"/"}`, `"$input/"`},
		{"throwing-exists", `{"op":"exists","source":{"op":"read","path":"/a/x"}}`, `{"op":"exists","source":"$input/a/x"}`},
		{"ordered-fields", `{"op":"object","fields":{"first":{"op":"read","path":"/a/x"},"second":{"op":"read","path":"/b"}}}`, `{"op":"object","fields":{"first":"$input/a/x","second":"$input/b"}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var plans [2]*compiledOutputPlan
			var counts [2][2]int
			for index, source := range []string{fixture.object, fixture.compact} {
				compiler := newExpressionCompiler(DefaultLimits(), nil)
				var err error
				plans[index], err = compiler.compilePlan(decodeCompactFixture(t, source), "/program", scope, 1)
				if err != nil {
					t.Fatal(err)
				}
				counts[index] = [2]int{compiler.nodes, compiler.maximumDepth}
			}
			if counts[0] != counts[1] {
				t.Fatal("compact syntax changed compiled node/depth accounting", counts)
			}
			for _, raw := range []string{`{}`, `null`, `false`, `{"a":null}`, `{"a":false}`, `{"a":{"x":null}}`, `{"a":{"x":9007199254740993},"b":false}`, `{"a":{"x":"long <payload>"},"a/b":{"~x":false},"":"empty-key"}`} {
				input, err := ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				for _, boundary := range []struct{ bytes, steps, cancel int }{{1, 0, 100}, {8, 0, 100}, {1024, 1, 100}, {1024, 2, 100}, {1024, 0, 1}, {1024, 0, 2}, {1024, 0, 3}, {1024, 0, 100}} {
					limits := DefaultLimits()
					limits.BufferBytes = boundary.bytes
					for _, isPlanned := range []bool{false, true} {
						before := evaluateCompactFixture(t, plans[0], input, limits, boundary.steps, boundary.cancel, isPlanned)
						after := evaluateCompactFixture(t, plans[1], input, limits, boundary.steps, boundary.cancel, isPlanned)
						if !reflect.DeepEqual(before, after) {
							t.Fatalf("input=%s boundary=%+v planned=%t: before=%+v after=%+v", raw, boundary, isPlanned, before, after)
						}
					}
				}
			}
		})
	}
}

func TestCompactReadRetainsCompileScopeAndLimits(t *testing.T) {
	for _, fixture := range []struct{ object, compact string }{
		{`{"op":"read","from":"item"}`, `"$item"`},
		{`{"op":"read","from":"binding","name":"missing"}`, `"$binding/missing"`},
		{`{"op":"read","from":"runtime","path":"/scope/model"}`, `"$runtime/scope/model"`},
		{`{"op":"read","from":"state","path":"/missing"}`, `"$state/missing"`},
		{`{"op":"read","from":"state"}`, `"$state"`},
	} {
		var reasons [2]string
		for index, source := range []string{fixture.object, fixture.compact} {
			_, err := newExpressionCompiler(DefaultLimits(), nil).compilePlan(decodeCompactFixture(t, source), "/program", expressionScope{}, 1)
			if err == nil {
				t.Fatal("scope restriction bypassed", source)
			}
			reasons[index] = err.Error()
		}
		if reasons[0] != reasons[1] {
			t.Fatal("compile error location or reason changed", reasons)
		}
	}
	for _, source := range []string{`{"op":"exists","source":{"op":"read"}}`, `{"op":"exists","source":"$input"}`} {
		limits := DefaultLimits()
		limits.Depth = 1
		if _, err := newExpressionCompiler(limits, nil).compilePlan(decodeCompactFixture(t, source), "/program", expressionScope{}, 1); err == nil {
			t.Fatal("compact syntax bypassed compiled depth", source)
		}
		limits = DefaultLimits()
		limits.Nodes = 1
		if _, err := newExpressionCompiler(limits, nil).compilePlan(decodeCompactFixture(t, source), "/program", expressionScope{}, 1); err == nil {
			t.Fatal("compact syntax bypassed compiled nodes", source)
		}
	}
}

func TestCompactReadPackageRoundtripAndEvidence(t *testing.T) {
	document := schemaPackageFixture(t)
	before := compilePackageForTest(t, document)
	beforeReport := Verify(t.Context(), before)
	if !beforeReport.Passed {
		t.Fatal("baseline fixture did not verify", beforeReport.Issues)
	}
	entry := document.Mapping.Directions[DecodeRequest]
	entry.Program.Fields["model"] = decodeCompactFixture(t, `"$input/model"`)
	document.Mapping.Directions[DecodeRequest] = entry
	compact := compilePackageForTest(t, document)
	spec, err := EncodeValue(document.Spec)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := EncodeValue(document.Mapping)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fromSources, issues := compiler.CompileSources(spec.Bytes(), mapping.Bytes())
	if err := IssuesError(issues); err != nil || fromSources == nil {
		t.Fatal("separate compact sources did not compile", err)
	}
	if fromSources.Hash() != compact.Hash() {
		t.Fatal("source/package compilation produced different normalized revisions")
	}
	encodedCompact, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	meter := valueTokenMeter{limits: DefaultLimits(), domain: "fixture"}
	if err := meter.scan(encodedCompact.JSONString()); err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.Nodes = meter.nodes
	bounded, err := NewCompiler(limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, useSources := range []bool{false, true} {
		var compiled *Compiled
		var issues []ConversionIssue
		if useSources {
			compiled, issues = bounded.CompileSources(spec.Bytes(), mapping.Bytes())
		} else {
			compiled, issues = bounded.CompilePackage(encodedCompact.Bytes())
		}
		if compiled == nil || IssuesError(issues) != nil {
			t.Fatal("compact syntax exceeded its exact source-token boundary", useSources, issues)
		}
		returned, _ := compiled.Package()
		roundtrip, err := EncodeValue(returned)
		if err != nil {
			t.Fatal(err)
		}
		if result, issues := bounded.CompilePackage(roundtrip.Bytes()); result == nil || IssuesError(issues) != nil {
			t.Fatal("round trip expanded beyond the source-token boundary", issues)
		}
	}
	if result, issues := bounded.CompilePackage(before.definition.Bytes()); result != nil || IssuesError(issues) == nil {
		t.Fatal("object syntax bypassed its original source-token charge", issues)
	}
	if compact.Hash() == before.Hash() || beforeReport.IsCurrent(compact.Hash(), CompilerVersion, compact.SamplesHash()) {
		t.Fatal("authored syntax change reused old revision evidence")
	}
	if compact.SamplesHash() != before.SamplesHash() {
		t.Fatal("compact authoring changed declared samples")
	}
	for range 3 {
		returned, ok := compact.Package()
		if !ok || !returned.Mapping.Directions[DecodeRequest].Program.Fields["model"].isCompactRead {
			t.Fatal("Package expanded compact syntax")
		}
		encoded, err := EncodeValue(returned)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := DecodeProtocolPackage(encoded.Bytes(), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		recompiled := compilePackageForTest(t, parsed)
		if recompiled.Hash() != compact.Hash() {
			t.Fatal("package parse/encode/recompile changed revision identity")
		}
		compact = recompiled
	}
	report := Verify(t.Context(), compact)
	if !report.Passed || !report.IsCurrent(compact.Hash(), CompilerVersion, compact.SamplesHash()) {
		t.Fatal("compact package did not obtain its own valid evidence", report.Issues)
	}
	returned, _ := compact.Package()
	returned.Spec.Name += " edited"
	edited := compilePackageForTest(t, returned)
	if report.IsCurrent(edited.Hash(), CompilerVersion, edited.SamplesHash()) {
		t.Fatal("subsequent edit retained stale evidence")
	}
	editedPackage, _ := edited.Package()
	if !editedPackage.Mapping.Directions[DecodeRequest].Program.Fields["model"].isCompactRead {
		t.Fatal("unrelated authoring edit expanded a compact read")
	}
}

func TestCompactReadPlanSurvivesGraphRelease(t *testing.T) {
	input, err := ParseValue([]byte(`{"value":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`{"op":"object","fields":{"value":{"op":"read","path":"/value"}}}`, `{"op":"object","fields":{"value":"$input/value"}}`} {
		compiler := newExpressionCompiler(DefaultLimits(), nil)
		plan, err := compiler.compilePlan(decodeCompactFixture(t, source), "/program", expressionScope{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		before := evaluateCompactFixture(t, plan, input, DefaultLimits(), 0, 100, true)
		compiler.releaseGraph()
		after := evaluateCompactFixture(t, plan, input, DefaultLimits(), 0, 100, true)
		if before.reason != "<nil>" || !reflect.DeepEqual(before, after) {
			t.Fatal("released authoring graph changed read execution", before, after)
		}
	}
}
