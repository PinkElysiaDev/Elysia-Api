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
