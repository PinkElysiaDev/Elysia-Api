package protocol

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
)

var deepPackageNames = []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"}

func readDeepPackage(tb testing.TB, name string) ([]byte, []byte) {
	tb.Helper()
	read := func(part string) []byte {
		raw, err := os.ReadFile(filepath.Join("builtin", "packages", name, part+".json"))
		if err != nil {
			tb.Fatal(err)
		}
		return raw
	}
	return read("spec"), read("mapping")
}

func deepCompiler(tb testing.TB) *Compiler {
	tb.Helper()
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return compiler
}

func decodeDeepPackage(tb testing.TB, spec, mapping []byte) ProtocolPackage {
	tb.Helper()
	document := ProtocolPackage{SchemaVersion: ProtocolPackageSchemaVersion}
	if err := decodeContract(spec, &document.Spec); err != nil {
		tb.Fatal(err)
	}
	if err := decodeContract(mapping, &document.Mapping); err != nil {
		tb.Fatal(err)
	}
	return document
}

// These stages separate authored decoding, contract compilation and route
// composition. Whole-package and HTTP benchmarks remain the acceptance gates.
func BenchmarkDeepCompilation(b *testing.B) {
	for _, name := range deepPackageNames {
		spec, mapping := readDeepPackage(b, name)
		b.Run(name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				decodeDeepPackage(b, spec, mapping)
			}
		})
		b.Run(name+"/contract", func(b *testing.B) {
			document := decodeDeepPackage(b, spec, mapping)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := document.compileContract(DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/policy", func(b *testing.B) {
			compiled, issues := deepCompiler(b).CompileSources(spec, mapping)
			if err := IssuesError(issues); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDeepExpressionDecode(b *testing.B) {
	for _, depth := range []int{1, 16, 32} {
		raw := `{"op":"read","path":"/value"}`
		for range depth {
			raw = `{"op":"let","name":"value","source":{"op":"literal","value":false},"body":` + raw + `}`
		}
		b.Run("nested-"+strconv.Itoa(depth), func(b *testing.B) {
			input := []byte(raw)
			b.ReportAllocs()
			for b.Loop() {
				var expression Expression
				if err := decodeContract(input, &expression); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDeepScalarReads(b *testing.B) {
	read := Expression{Op: "read", Path: "/payload"}
	for _, operation := range []string{"exists", "present", "type", "is_integer", "equal"} {
		for _, scoped := range []bool{false, true} {
			name := operation
			expression := Expression{Op: operation, Source: &read}
			if operation == "equal" {
				expression.Source = nil
				expression.Items = []Expression{read, {Op: "literal", Value: Value{raw: `"hello"`}}}
			}
			if scoped {
				name += "-scope"
				body := expression
				expression = Expression{Op: "with_input", Source: &Expression{Op: "read", Path: "/nested"}, Body: &body}
			}
			b.Run(name, func(b *testing.B) {
				compiler := newExpressionCompiler(DefaultLimits(), nil)
				plan, err := compiler.compilePlan(expression, "/program", expressionScope{}, 1)
				if err != nil {
					b.Fatal(err)
				}
				compiler.releaseGraph()
				input := Value{raw: `{"payload":"hello","nested":{"payload":"hello"}}`}
				b.ReportAllocs()
				for b.Loop() {
					view := newInputView(input)
					result, err := plan.evaluate(evaluation{ctx: b.Context(), input: input, root: input, inputView: view, objectReader: view.objectReader(), valueReader: &view.owned, budget: &evaluationBudget{limits: DefaultLimits()}})
					if err != nil || result.IsZero() {
						b.Fatal("scalar read failed", result, err)
					}
				}
			})
		}
	}
}

func BenchmarkDeepCoreObjects(b *testing.B) {
	output := JSONLeaf(Value{raw: `{"usage":{"input":{"count":7}},"usage":{"input":{"origin":"observed"},"output":{"count":2,"origin":"observed"}}}`})
	b.ReportAllocs()
	for b.Loop() {
		var event Event
		if err := writeCoreEvent(output, &event, &valueReader{}); err != nil || event.Usage == nil || event.Usage.Input.Count != 7 {
			b.Fatal("repeated core objects lost fields", event.Usage, err)
		}
	}
}

// Run in a fresh process. The first compile warms codec/schema machinery; the
// retained measurements then keep exactly four packages and sixteen policies.
func TestDeepCompilationDiagnostics(t *testing.T) {
	if os.Getenv("ELYSIA_DEEP_DIAGNOSTICS") != "1" {
		t.Skip("opt-in retained graph diagnostic")
	}
	compiler := deepCompiler(t)
	compile := func() []*Compiled {
		var packages []*Compiled
		for _, name := range deepPackageNames {
			spec, mapping := readDeepPackage(t, name)
			compiled, issues := compiler.CompileSources(spec, mapping)
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			packages = append(packages, compiled)
		}
		return packages
	}
	compile()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	packages := compile()
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("packages HeapAlloc=%d delta=%d graph=%v", after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc), inspectDeepGraph(packages))
	var policies []*CompiledConversion
	for _, source := range packages {
		for _, target := range packages {
			policy, err := ResolveConversion(DefaultConversionPolicy(source, target))
			if err != nil {
				t.Fatal(err)
			}
			policies = append(policies, policy)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("packages+policies HeapAlloc=%d delta=%d graph=%v", after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc), inspectDeepGraph([]any{packages, policies}))
	runtime.KeepAlive(packages)
	runtime.KeepAlive(policies)
}

func inspectDeepGraph(root any) map[string]int {
	counts := map[string]int{}
	seen := map[uintptr]bool{}
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				visit(value.Elem())
			}
		case reflect.Pointer:
			if value.IsNil() || seen[value.Pointer()] {
				return
			}
			seen[value.Pointer()] = true
			kind := value.Type().Elem()
			if kind == reflect.TypeFor[compiledExpression]() || kind == reflect.TypeFor[compiledOutputPlan]() || kind == reflect.TypeFor[expressionCompileFacts]() {
				counts[kind.Name()]++
				counts[kind.Name()+"-shallow-bytes"] += int(kind.Size())
			}
			visit(value.Elem())
		case reflect.Struct:
			for index := range value.NumField() {
				visit(value.Field(index))
			}
		case reflect.Slice, reflect.Array:
			for index := range value.Len() {
				visit(value.Index(index))
			}
		case reflect.Map:
			iterator := value.MapRange()
			for iterator.Next() {
				visit(iterator.Value())
			}
		}
	}
	visit(reflect.ValueOf(root))
	return counts
}
