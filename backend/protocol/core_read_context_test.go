package protocol

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func createMemoSource() Provenance {
	return Provenance{Protocol: Identity{Family: "fixture", WireVersion: "1", DefinitionID: "definition", Revision: "revision"},
		Direction: DecodeResponse, Path: "/content/0", Scope: Scope{Provider: "provider", Account: "account", Model: "model", Session: "session"}}
}

func assertMemoOutput(t *testing.T, actual, expected JSONOutput, actualErr, expectedErr error) {
	t.Helper()
	if fmt.Sprint(actualErr) != fmt.Sprint(expectedErr) || reflect.TypeOf(actualErr) != reflect.TypeOf(expectedErr) {
		t.Fatal("reader error changed", actualErr, expectedErr)
	}
	if actualErr != nil {
		return
	}
	got, gotErr := actual.Encode(t.Context())
	want, wantErr := expected.Encode(t.Context())
	if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || normalizedOutputBytes(actual) != normalizedOutputBytes(expected) {
		t.Fatal("reader bytes or logical size changed", gotErr, wantErr)
	}
}

func TestCoreReadMemoCardinalityAndKeys(t *testing.T) {
	for _, count := range []int{0, 1, provenanceReadCapacity, provenanceReadCapacity + 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			memo := &provenanceReadMemo{}
			reader := coreReadContext{provenance: memo}
			var outputs []JSONOutput
			for index := range count {
				source := createMemoSource()
				source.Path = fmt.Sprint("/", index)
				got, gotErr := reader.readProvenance(source, nil)
				want, wantErr := readCoreProvenance(source, nil, nil)
				assertMemoOutput(t, got, want, gotErr, wantErr)
				outputs = append(outputs, got)
			}
			if memo.count != min(count, provenanceReadCapacity) || (memo.entries == nil) != (count == 0) {
				t.Fatal("memo capacity or lazy allocation changed")
			}
			for index, first := range outputs {
				source := createMemoSource()
				source.Path = fmt.Sprint("/", index)
				got, err := reader.readProvenance(source, nil)
				if err != nil || got.node == first.node {
					t.Fatal("reused mutable root", err)
				}
				if index < provenanceReadCapacity && got.node.fields[0].value.node != first.node.fields[0].value.node {
					t.Fatal("bounded repeated tuple did not share immutable children")
				}
				if index >= provenanceReadCapacity && got.node.fields[0].value.node == first.node.fields[0].value.node {
					t.Fatal("overflow tuple was retained")
				}
			}
			memo.release()
			if memo.entries != nil || memo.count != 0 {
				t.Fatal("memo retained entries after release")
			}
		})
	}
	for name, mutate := range map[string]func(*Provenance){
		"family":     func(value *Provenance) { value.Protocol.Family += "-next" },
		"wire":       func(value *Provenance) { value.Protocol.WireVersion += "-next" },
		"definition": func(value *Provenance) { value.Protocol.DefinitionID += "-next" },
		"revision":   func(value *Provenance) { value.Protocol.Revision += "-next" },
		"direction":  func(value *Provenance) { value.Direction = DecodeRequest },
		"path":       func(value *Provenance) { value.Path = "/escaped~1name/~0token" },
		"provider":   func(value *Provenance) { value.Scope.Provider += "-next" },
		"account":    func(value *Provenance) { value.Scope.Account += "-next" },
		"model":      func(value *Provenance) { value.Scope.Model += "-next" },
		"session":    func(value *Provenance) { value.Scope.Session += "-next" },
	} {
		t.Run(name, func(t *testing.T) {
			reader := coreReadContext{provenance: &provenanceReadMemo{}}
			source := createMemoSource()
			first, err := reader.readProvenance(source, nil)
			if err != nil {
				t.Fatal(err)
			}
			copied := source
			copied.Scope.Model = strings.Clone(copied.Scope.Model)
			equal, err := reader.readProvenance(copied, nil)
			if err != nil || equal.node.fields[0].value.node != first.node.fields[0].value.node {
				t.Fatal("equal tuple storage should not change key", err)
			}
			mutate(&source)
			got, gotErr := reader.readProvenance(source, nil)
			want, wantErr := readCoreProvenance(source, nil, nil)
			assertMemoOutput(t, got, want, gotErr, wantErr)
			if reader.provenance.count != 2 {
				t.Fatal("complete provenance key omitted a field")
			}
		})
	}
}

func TestCoreReadMemoSelectedAndNormalizationParity(t *testing.T) {
	for _, hasCache := range []bool{false, true} {
		t.Run(fmt.Sprint(hasCache), func(t *testing.T) {
			var original, baseline *semanticCache
			if hasCache {
				original, baseline = &semanticCache{limits: DefaultLimits()}, &semanticCache{limits: DefaultLimits()}
			}
			memo := &provenanceReadMemo{}
			reader := coreReadContext{cache: original, provenance: memo}
			source := createMemoSource()
			payload := Value{raw: ` {"text":"` + strings.Repeat("<>&", semanticCacheMinBytes) + `","decimal":9007199254740993.25} `}
			response := &Response{Content: []Node{{Kind: TextNode, Payload: payload, Source: &source, Native: &Native{Source: source, Value: payload}}}}
			for _, pointer := range [][]string{{"path"}, {"protocol", "family"}, {"scope"}, {"scope", "account", "bad"}, {"unknown"}} {
				got, gotErr := reader.readProvenance(source, pointer)
				want, wantErr := readCoreProvenance(source, pointer, baseline)
				assertMemoOutput(t, got, want, gotErr, wantErr)
			}
			if memo.entries != nil {
				t.Fatal("selected reads allocated a provenance memo")
			}
			for range 2 {
				for _, pointer := range [][]string{nil, {"content", "0"}, {"content", "bad-index"}, {"content", "0", "source", "path"}} {
					got, gotErr := reader.readResponsePointer(response, pointer)
					want, wantErr := readCoreResponseOutput(response, pointer, baseline)
					assertMemoOutput(t, got, want, gotErr, wantErr)
				}
			}
			if !reflect.DeepEqual(original, baseline) {
				t.Fatal("memo changed original nil or populated normalization cache")
			}
			response.Content[0].Native.Value = Value{}
			for range 2 {
				got, gotErr := reader.readResponsePointer(response, nil)
				want, wantErr := readCoreResponseOutput(response, nil, baseline)
				assertMemoOutput(t, got, want, gotErr, wantErr)
				if gotErr == nil {
					t.Fatal("invalid opaque native leaf was accepted")
				}
			}
		})
	}
}

func TestCoreReadMemoOwnershipAndAuthority(t *testing.T) {
	source := createMemoSource()
	memo := &provenanceReadMemo{}
	reader := coreReadContext{provenance: memo}
	first, err := reader.readProvenance(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := first.Encode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.node.origin = capturedProvenanceOrigin
	second, err := reader.readProvenance(source, nil)
	if err != nil || second.node == first.node || second.node.origin != 0 {
		t.Fatal("memo copied source authority", err)
	}
	second.node.origin = capturedProvenanceOrigin
	third, err := reader.readProvenance(source, nil)
	if err != nil || third.node.origin != 0 {
		t.Fatal("memo stored returned mutable root", err)
	}
	var decoded Provenance
	if err := writeCoreProvenance(third, &decoded, &valueReader{requireCapturedOrigins: true}); err == nil {
		t.Fatal("ordinary tuple forged a captured receipt")
	}
	if err := writeCoreProvenance(first, &decoded, &valueReader{requireCapturedOrigins: true}); err != nil {
		t.Fatal(err)
	}
	fields, err := plannedObjectFields(third)
	if err != nil {
		t.Fatal(err)
	}
	fields[0].value = JSONLeaf(StringValue("changed-local-projection"))
	source.Path, source.Scope.Account = "/changed", "changed-account"
	memo.release()
	actual, err := third.Encode(t.Context())
	if err != nil || actual != expected {
		t.Fatal("released memo or caller edit changed retained immutable output", err)
	}
	captured, err := readCoreProvenance(createMemoSource(), nil, nil)
	if err != nil || captured.node == third.node || captured.node.fields[0].value.node == third.node.fields[0].value.node {
		t.Fatal("legacy capture reader entered response memo", err)
	}
}

func TestCoreReadMemoOccurrenceBudgetsAndCancellation(t *testing.T) {
	source := createMemoSource()
	response := &Response{Content: []Node{{Kind: TextNode, Source: &source}}}
	read := readExpression("input", "/content/0/source")
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "array", Items: []Expression{read, read}}, "/memo-budget", expressionScope{coreInput: reflect.TypeFor[Response]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	single, err := readCoreProvenance(source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name         string
		steps, bytes int
		isCanceled   bool
	}{
		{"normal", DefaultLimits().Nodes, DefaultLimits().BufferBytes, false},
		{"steps", 2, DefaultLimits().BufferBytes, false},
		{"bytes", DefaultLimits().Nodes, normalizedOutputBytes(single) - 1, false},
		{"canceled", DefaultLimits().Nodes, DefaultLimits().BufferBytes, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := t.Context()
			if scenario.isCanceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			run := func(memo *provenanceReadMemo) (plannedOutput, int, error) {
				reader := coreReadContext{provenance: memo}
				read := func(pointer []string) (JSONOutput, error) { return reader.readResponsePointer(response, pointer) }
				limits := DefaultLimits()
				limits.BufferBytes = scenario.bytes
				state := evaluation{ctx: ctx, inputReader: read, rootReader: read, valueReader: &valueReader{}, budget: &evaluationBudget{limits: limits, stepLimit: scenario.steps}}
				output, err := plan.build(state)
				return output, state.budget.steps, err
			}
			want, wantSteps, wantErr := run(nil)
			got, gotSteps, gotErr := run(&provenanceReadMemo{})
			if gotSteps != wantSteps || got.bytes != want.bytes {
				t.Fatal("memo skipped an occurrence budget")
			}
			assertMemoOutput(t, got.output, want.output, gotErr, wantErr)
			if scenario.name != "normal" && gotErr == nil {
				t.Fatal("expected original boundary refusal")
			}
		})
	}
}

func TestCoreReadMemoOwnerEffectsAndIsolation(t *testing.T) {
	source := createMemoSource()
	response := &Response{Content: []Node{{Kind: TextNode, Source: &source}}}
	read := readExpression("input", "/source")
	compiler := expressionCompiler{limits: DefaultLimits()}
	condition, err := compiler.compilePlan(Expression{Op: "exists", Source: &read}, "/owner-check", expressionScope{coreInput: reflect.TypeFor[Node]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &Compiled{}
	effect := compiledSemanticEffect{id: "owner-check", scope: nodeScope, kind: "reject", when: condition, reason: "expected"}
	run := func(memo *provenanceReadMemo) (int, error) {
		state := evaluation{ctx: t.Context(), coreDocument: response, valueReader: &valueReader{}, budget: &evaluationBudget{limits: DefaultLimits()}}
		err := compiled.checkOwnerEffect(EncodeResponse, state, effect, memo)
		return state.budget.steps, err
	}
	wantSteps, wantErr := run(nil)
	memo := &provenanceReadMemo{}
	for range 2 {
		gotSteps, gotErr := run(memo)
		if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || gotSteps != wantSteps || memo.count != 1 {
			t.Fatal("owner effect memo changed budget/error or was not propagated", gotErr, wantErr)
		}
	}
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	compiled = compilePackageForTest(t, document)
	options := EvaluationContext{Scope: Scope{Provider: "local", Account: "test"}}
	decoded, err := compiled.DecodeResponseValue(t.Context(), Value{raw: `{"reply":"hello"}`}, options)
	if err != nil {
		t.Fatal(err)
	}
	_, _, first, err := preparePlannedCoreEncoding(t.Context(), compiled, EncodeResponse, decoded, options)
	if err != nil {
		t.Fatal(err)
	}
	_, _, second, err := preparePlannedCoreEncoding(t.Context(), compiled, EncodeResponse, decoded, options)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first == second {
		t.Fatal("separate response invocations shared a memo")
	}
	first.release()
	second.release()
	raw, err := compiled.EncodeResponseValue(t.Context(), decoded, options)
	if err != nil || raw.JSONString() != `{"reply":"hello"}` {
		t.Fatal("native baseline reconciliation changed", err, raw)
	}
}

func TestCoreReadMemoInputReaderOwners(t *testing.T) {
	source := createMemoSource()
	payload := Value{raw: ` {"text":"` + strings.Repeat("<>&", semanticCacheMinBytes) + `"} `}
	nodes := []Node{{Kind: TextNode, Payload: payload, Source: &source}}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Content: nodes}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: nodes}
	event := &Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, Response: response}
	for _, fixture := range []struct {
		name      string
		input     any
		direction Direction
		hasOwner  bool
		read      func([]string, *semanticCache) (JSONOutput, error)
	}{
		{"request", request, EncodeRequest, false, func(pointer []string, cache *semanticCache) (JSONOutput, error) {
			return readCoreRequestOutput(request, pointer, cache)
		}},
		{"event", event, EncodeEvent, false, func(pointer []string, cache *semanticCache) (JSONOutput, error) {
			return readCoreEventOutput(event, pointer, cache)
		}},
		{"response", response, EncodeResponse, true, func(pointer []string, cache *semanticCache) (JSONOutput, error) {
			return readCoreResponseOutput(response, pointer, cache)
		}},
		{"response-other-direction", response, DecodeResponse, false, func(pointer []string, cache *semanticCache) (JSONOutput, error) {
			return readCoreResponseOutput(response, pointer, cache)
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, hasCache := range []bool{false, true} {
				var original, baseline *semanticCache
				if hasCache {
					original, baseline = &semanticCache{limits: DefaultLimits()}, &semanticCache{limits: DefaultLimits()}
				}
				read, memo := createCoreInputReader(fixture.input, fixture.direction, original)
				if (memo != nil) != fixture.hasOwner {
					t.Fatal("memo owner expanded beyond synchronous response encoding")
				}
				for range 2 {
					for _, pointer := range [][]string{nil, {"schemaVersion"}, {"content", "bad-index"}} {
						got, gotErr := read(pointer)
						want, wantErr := fixture.read(pointer, baseline)
						assertMemoOutput(t, got, want, gotErr, wantErr)
					}
				}
				if !reflect.DeepEqual(original, baseline) {
					t.Fatal("input reader changed original normalization cache")
				}
				if memo != nil && memo.count != 1 {
					t.Fatal("response full reads did not use their own memo")
				}
				memo.release()
			}
		})
	}
	read, memo := createCoreInputReader(struct{}{}, EncodeResponse, nil)
	if read == nil || memo != nil {
		t.Fatal("invalid input changed reader creation boundary")
	}
	if _, err := read(nil); err == nil || err.Error() != "core input plan requires a request, response or event" {
		t.Fatal("invalid input changed first-read failure", err)
	}
}

func TestCoreReadMemoPreparationFailure(t *testing.T) {
	document := textProtocolPackage(t)
	mapping := document.Mapping.Directions[EncodeResponse]
	mapping.Effects = append(mapping.Effects, ProtocolSemanticEffect{ID: "reject-source", Scope: nodeScope, Kind: "reject", Path: "/source",
		When: &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/source"))}, Reason: "fixture source is rejected"})
	document.Mapping.Directions[EncodeResponse] = mapping
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{}
	source := createMemoSource()
	response := &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"),
		Children: []Node{{Kind: TextNode, Payload: StringValue("hello"), Source: &source}},
	}}}
	for range 2 {
		state, limits, memo, err := preparePlannedCoreEncoding(t.Context(), compiled, EncodeResponse, response, options)
		if err == nil || memo != nil || !reflect.DeepEqual(state, evaluation{}) || limits != (Limits{}) {
			t.Fatal("failed response preparation published an owner or partial state", err)
		}
		if !strings.Contains(err.Error(), "fixture source is rejected") {
			t.Fatal("setup failure changed owner-effect error", err)
		}
	}
	response.Content[0].Children[0].Source = nil
	_, _, memo, err := preparePlannedCoreEncoding(t.Context(), compiled, EncodeResponse, response, options)
	if err != nil || memo == nil || memo.count != 0 || memo.entries != nil {
		t.Fatal("successful preparation inherited a failed response owner", err)
	}
	memo.release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, memo, err = preparePlannedCoreEncoding(ctx, compiled, EncodeResponse, response, options)
	if err == nil || memo != nil {
		t.Fatal("canceled preparation created an owner", err)
	}
}
