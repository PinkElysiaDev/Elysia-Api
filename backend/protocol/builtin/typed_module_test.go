package builtin

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

// jsonOnlyModule deliberately hides the typed interfaces to exercise the same
// adapter through its public JSON contract and detect optimization divergence.
type jsonOnlyModule struct{ p.Module }

func (wrapped jsonOnlyModule) NewStream(direction p.Direction, limits p.Limits) (p.Module, error) {
	stream, err := wrapped.Module.(p.StreamModule).NewStream(direction, limits)
	return jsonOnlyModule{stream}, err
}

// Keep the installed wire validators while hiding only typed event decoding.
// This exercises the previous final-validation boundary on identical frames.
type jsonEventFactory struct{ module }
type jsonEventStream struct{ p.Module }

func (factory jsonEventFactory) NewStream(direction p.Direction, limits p.Limits) (p.Module, error) {
	stream, err := factory.module.NewStream(direction, limits)
	return jsonEventStream{stream}, err
}

func (stream jsonEventStream) ValidateWireEvent(frame p.Value) error {
	return stream.Module.(p.WireEventValidator).ValidateWireEvent(frame)
}

func TestFinalWireValidationTypedParity(t *testing.T) {
	t.Parallel()
	modules := Modules()
	for i, adapter := range modules {
		modules[i] = jsonEventFactory{adapter.(module)}
	}
	genericCompiler, err := p.NewCompiler(p.DefaultLimits(), modules, nil)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range definitions {
		generic, issues := genericCompiler.Compile(raw.Bytes())
		if generic == nil {
			t.Fatal(issues)
		}
		typed := testCompiled(t, generic.Codec(p.DecodeEvent))
		for _, sample := range generic.Definition().Samples {
			if sample.Direction != p.DecodeEvent {
				continue
			}
			t.Run(generic.Identity().DefinitionID+"/"+sample.ID, func(t *testing.T) {
				frames := []p.Value{sample.Input}
				if sample.Sequence {
					if err := sample.Input.Decode(&frames); err != nil {
						t.Fatal(err)
					}
				}
				observe := func(compiled *p.Compiled) []string {
					validation, err := compiled.NewWireStreamValidation(sample.Scope)
					if err != nil {
						t.Fatal(err)
					}
					var result []string
					for _, frame := range frames {
						err = validation.Consume(t.Context(), frame)
						result = append(result, fmt.Sprint(err))
						if err != nil {
							return result
						}
					}
					return append(result, fmt.Sprint(validation.Finish()))
				}
				if before, after := observe(generic), observe(typed); !reflect.DeepEqual(before, after) {
					t.Fatalf("final wire validation differs\nJSON: %v\ntyped: %v", before, after)
				}
			})
		}
	}
}

func BenchmarkFinalWireValidation(b *testing.B) {
	compiled := testCompiled(b, Chat)
	parse := func(delta, finish string) p.Value {
		value, err := p.ParseValue([]byte(chatContractFrame("r", "m", 1, delta, finish)))
		if err != nil {
			b.Fatal(err)
		}
		return value
	}
	start := parse(`{"role":"assistant"}`, `null`)
	delta := parse(`{"content":"hello"}`, `null`)
	end := parse(`{}`, `"stop"`)
	b.ReportAllocs()
	for b.Loop() {
		validation, err := compiled.NewWireStreamValidation(p.Scope{})
		if err != nil {
			b.Fatal(err)
		}
		if err := validation.Consume(b.Context(), start); err != nil {
			b.Fatal(err)
		}
		for range 256 {
			if err := validation.Consume(b.Context(), delta); err != nil {
				b.Fatal(err)
			}
		}
		if err := validation.Consume(b.Context(), end); err != nil {
			b.Fatal(err)
		}
		if err := validation.Finish(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRequestWireSchemaTypedParity(t *testing.T) {
	t.Parallel()
	definitions, _ := Definitions()
	genericModules := Modules()
	for index := range genericModules {
		genericModules[index] = jsonOnlyModule{genericModules[index]}
	}
	typedCompiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	genericCompiler, _ := p.NewCompiler(p.DefaultLimits(), genericModules, nil)
	for _, raw := range definitions {
		var original p.Definition
		if err := sonic.ConfigStd.Unmarshal(raw.Bytes(), &original); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"wire", "semantic", "after", "reject-input", "reject-output"} {
			t.Run(original.ID+"/"+mode, func(t *testing.T) {
				var definition p.Definition
				_ = sonic.ConfigStd.Unmarshal(raw.Bytes(), &definition)
				decode, encode := definition.Directions[p.DecodeRequest], definition.Directions[p.EncodeRequest]
				wire := &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true}
				decode.Input, encode.Output = wire, wire
				switch mode {
				case "semantic":
					decode.Output, encode.Input = wire, wire
				case "after":
					decode.After, encode.After = &p.Expression{Op: "read"}, &p.Expression{Op: "read"}
				case "reject-input":
					decode.Input = &p.ValueSchema{Type: p.ObjectType, Properties: map[string]p.ValueSchema{"synthetic_required": {Type: p.StringType}}, Required: []string{"synthetic_required"}, AllowUnknown: true}
				case "reject-output":
					encode.Output = &p.ValueSchema{Type: p.ObjectType, Properties: map[string]p.ValueSchema{"synthetic_required": {Type: p.StringType}}, Required: []string{"synthetic_required"}, AllowUnknown: true}
				}
				definition.Directions[p.DecodeRequest], definition.Directions[p.EncodeRequest] = decode, encode
				wireDefinition, _ := sonic.ConfigStd.Marshal(definition)
				typed, issues := typedCompiler.Compile(wireDefinition)
				if typed == nil {
					t.Fatal(issues)
				}
				generic, issues := genericCompiler.Compile(wireDefinition)
				if generic == nil {
					t.Fatal(issues)
				}
				for _, sample := range definition.Samples {
					// Schema/after dispatch is independent of the request payload.
					// The full sample catalog is verified separately.
					if sample.ID != "text-decode" {
						continue
					}
					options := p.EvaluationContext{Values: sample.Context, Scope: sample.Scope}
					before := observeRequestPipeline(t, generic, sample.Input.Bytes(), options)
					after := observeRequestPipeline(t, typed, sample.Input.Bytes(), options)
					if !reflect.DeepEqual(before, after) {
						t.Fatalf("%s differs\ngeneric=%+v\ntyped=%+v", sample.ID, before, after)
					}
					return
				}
				t.Fatal("missing text request fixture")
			})
		}
	}
}

func TestResponsesNullableHistoryRetainsAuthoredBoundaries(t *testing.T) {
	const body = `{"model":"m","input":[` + nullableReasoningItem + `]}`
	for _, mode := range []string{"custom", "generic-module", "semantic-schema", "after", "reject-input", "replace-output"} {
		t.Run(mode, func(t *testing.T) {
			definition := testCompiled(t, Responses).Definition()
			definition.ID = "nullable-history-" + mode
			decode, encode := definition.Directions[p.DecodeRequest], definition.Directions[p.EncodeRequest]
			modules := Modules()
			switch mode {
			case "generic-module":
				for i := range modules {
					modules[i] = jsonOnlyModule{modules[i]}
				}
			case "semantic-schema":
				decode.Output = &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true}
				encode.Input = &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true}
			case "after":
				decode.After, encode.After = &p.Expression{Op: "read"}, &p.Expression{Op: "read"}
			case "reject-input":
				decode.Input = &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true, Properties: map[string]p.ValueSchema{
					"input": {Type: p.ArrayType, Items: &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true, Properties: map[string]p.ValueSchema{"content": {Type: p.ArrayType}}}},
				}}
			case "replace-output":
				encode.After = &p.Expression{Op: "literal", Value: testValue(t, strings.Replace(body, `"content":null`, `"content":{}`, 1))}
			}
			definition.Directions[p.DecodeRequest], definition.Directions[p.EncodeRequest] = decode, encode
			raw, err := p.EncodeValue(definition)
			if err != nil {
				t.Fatal(err)
			}
			compiler, err := p.NewCompiler(p.DefaultLimits(), modules, nil)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(raw.Bytes())
			if compiled == nil {
				t.Fatal(issues)
			}
			options := p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
			request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
			if mode == "reject-input" {
				if err == nil {
					t.Fatal("nullable history bypassed the author's array-only input schema")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replace-output" {
				request.Native = nil // Check exactly what the authored encoder emits.
			}
			wire, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replace-output" {
				if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err == nil {
					t.Fatal("request compatibility hid a non-array authored output")
				}
				return
			}
			sameJSON(t, wire, body)
		})
	}
}

func (wrapped jsonOnlyModule) Finish(ctx context.Context, options p.EvaluationContext) ([]p.Value, error) {
	if finalizer, ok := wrapped.Module.(p.StreamFinalizer); ok {
		return finalizer.Finish(ctx, options)
	}
	return nil, nil
}

func TestJSONModuleFallbackMatchesShippedSamples(t *testing.T) {
	t.Parallel()
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	modules := Modules()
	for i := range modules {
		modules[i] = jsonOnlyModule{modules[i]}
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), modules, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range definitions {
		generic, issues := compiler.Compile(raw.Bytes())
		if generic == nil {
			t.Fatal(issues)
		}
		t.Run(generic.Identity().DefinitionID, func(t *testing.T) {
			t.Parallel()
			if report := p.Verify(t.Context(), generic); !report.Passed {
				t.Fatal(report.Issues)
			}
		})
	}
}
