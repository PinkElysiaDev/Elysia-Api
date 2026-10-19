package builtin

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// jsonOnlyModule deliberately hides the typed interfaces to exercise the same
// adapter through its public JSON contract and detect optimization divergence.
type jsonOnlyModule struct{ p.Module }

func (wrapped jsonOnlyModule) NewStream(direction p.Direction, limits p.Limits) (p.Module, error) {
	stream, err := wrapped.Module.(p.StreamModule).NewStream(direction, limits)
	return jsonOnlyModule{stream}, err
}

func TestRequestWireSchemaTypedParity(t *testing.T) {
	definitions, _ := Definitions()
	genericModules := Modules()
	for index := range genericModules {
		genericModules[index] = jsonOnlyModule{genericModules[index]}
	}
	typedCompiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	genericCompiler, _ := p.NewCompiler(p.DefaultLimits(), genericModules, nil)
	for _, raw := range definitions {
		var original p.Definition
		if err := json.Unmarshal(raw.Bytes(), &original); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"wire", "semantic", "after", "reject-input", "reject-output"} {
			t.Run(original.ID+"/"+mode, func(t *testing.T) {
				var definition p.Definition
				_ = json.Unmarshal(raw.Bytes(), &definition)
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
				wireDefinition, _ := json.Marshal(definition)
				typed, issues := typedCompiler.Compile(wireDefinition)
				if typed == nil {
					t.Fatal(issues)
				}
				generic, issues := genericCompiler.Compile(wireDefinition)
				if generic == nil {
					t.Fatal(issues)
				}
				for _, sample := range definition.Samples {
					if sample.Direction != p.DecodeRequest {
						continue
					}
					options := p.EvaluationContext{Values: sample.Context, Scope: sample.Scope}
					before := observeMemoryRequest(t, generic, sample.Input.Bytes(), options, true)
					after := observeMemoryRequest(t, typed, sample.Input.Bytes(), options, true)
					if !reflect.DeepEqual(before, after) {
						t.Fatalf("%s differs\ngeneric=%+v\ntyped=%+v", sample.ID, before, after)
					}
				}
			})
		}
	}
}

func (wrapped jsonOnlyModule) Finish(ctx context.Context, options p.EvaluationContext) ([]p.Value, error) {
	if finalizer, ok := wrapped.Module.(p.StreamFinalizer); ok {
		return finalizer.Finish(ctx, options)
	}
	return nil, nil
}

func TestTypedModulesMatchJSONContractAndDeclarations(t *testing.T) {
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
	typedCompiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range definitions {
		generic, issues := compiler.Compile(raw.Bytes())
		if generic == nil {
			t.Fatal(issues)
		}
		typed, issues := typedCompiler.Compile(raw.Bytes())
		if typed == nil {
			t.Fatal(issues)
		}
		t.Run(typed.Identity().DefinitionID, func(t *testing.T) {
			// Verify covers expected wires, semantic roundtrips, event lifecycles,
			// native extension replay, TTL, long integers and explicit zero usage.
			for _, compiled := range []*p.Compiled{generic, typed} {
				if report := p.Verify(t.Context(), compiled); !report.Passed {
					t.Fatal(report.Issues)
				}
			}
			for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest, p.DecodeResponse, p.EncodeResponse} {
				definition := typed.Definition()
				mapping := definition.Directions[direction]
				mapping.After = &p.Expression{Op: "read"}
				definition.Directions[direction] = mapping
				value, _ := json.Marshal(definition)
				compiled, issues := typedCompiler.Compile(value)
				if compiled == nil {
					t.Fatal(issues)
				}
				if report := p.Verify(t.Context(), compiled); !report.Passed {
					t.Fatalf("post-mapping diverged: %v", report.Issues)
				}
			}
		})
	}
}
