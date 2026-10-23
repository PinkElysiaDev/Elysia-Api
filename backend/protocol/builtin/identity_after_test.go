package builtin

import (
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestIdentityAfterEncodingEligibility(t *testing.T) {
	for _, name := range []string{"absent", "identity", "explicit-input", "root", "required"} {
		t.Run(name, func(t *testing.T) {
			definition := testCompiled(t, Anthropic).Definition()
			for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
				mapping := definition.Directions[direction]
				after := &p.Expression{Op: "read"}
				switch name {
				case "absent":
					after = nil
				case "explicit-input":
					after.From = "input"
				case "root":
					after.From = "root"
				case "required":
					after.Required = true
				}
				mapping.After = after
				definition.Directions[direction] = mapping
			}
			compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := p.EncodeValue(definition)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(raw.Bytes())
			if compiled == nil {
				t.Fatal(issues)
			}
			want := name == "absent" || name == "identity" || name == "explicit-input"
			if compiled.CanReuseRequestEncoding() != want {
				t.Fatalf("encoding eligibility = %t, want %t", compiled.CanReuseRequestEncoding(), want)
			}
		})
	}
}

func TestIdentityAfterRequestSampleParity(t *testing.T) {
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	genericModules := Modules()
	for index := range genericModules {
		genericModules[index] = jsonOnlyModule{genericModules[index]}
	}
	typedCompiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	genericCompiler, _ := p.NewCompiler(p.DefaultLimits(), genericModules, nil)
	for _, raw := range definitions {
		var definition p.Definition
		if err := raw.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
			mapping := definition.Directions[direction]
			mapping.After = &p.Expression{Op: "read"}
			definition.Directions[direction] = mapping
		}
		wire, _ := p.EncodeValue(definition)
		typed, issues := typedCompiler.Compile(wire.Bytes())
		if typed == nil {
			t.Fatal(issues)
		}
		generic, issues := genericCompiler.Compile(wire.Bytes())
		if generic == nil {
			t.Fatal(issues)
		}
		for _, sample := range definition.Samples {
			if sample.Direction != p.DecodeRequest || sample.Sequence {
				continue
			}
			t.Run(definition.ID+"/"+sample.ID, func(t *testing.T) {
				options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
				before := observeRequestPipeline(t, generic, sample.Input.Bytes(), options)
				after := observeRequestPipeline(t, typed, sample.Input.Bytes(), options)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("identity after differs\ngeneric=%+v\ntyped=%+v", before, after)
				}
			})
		}
	}
}
