package builtin

import (
	"context"
	"encoding/json"
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
