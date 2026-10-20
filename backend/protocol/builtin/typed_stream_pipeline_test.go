package builtin

import (
	"fmt"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestTypedStreamConversionAndEncodingParity(t *testing.T) {
	definitions, _ := Definitions()
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
		typed := testCompiled(t, generic.Codec(p.EncodeEvent))
		for sourceName, streams := range map[string]map[string][]string{"text": textStreams, "tool": toolStreams} {
			for codec, frames := range streams {
				t.Run(sourceName+"/"+codec+"/"+typed.Codec(p.EncodeEvent), func(t *testing.T) {
					source := testCompiled(t, codec)
					conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(typed, source))
					if err != nil {
						t.Fatal(err)
					}
					route := conversion.VerificationRoute(source.Identity(), typed.Identity(), p.SSE)
					decode := p.EvaluationContext{State: p.NewEvaluationState()}
					before, after := p.EvaluationContext{State: p.NewEvaluationState()}, p.EvaluationContext{State: p.NewEvaluationState()}
					for _, wire := range frames {
						frame, err := source.DecodeFrame(t.Context(), testValue(t, wire), decode)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range frame.Events {
							original, _ := p.EncodeValue(event)
							oldSink, newSink := &p.DiagnosticSink{}, &p.DiagnosticSink{}
							oldValue, oldErr := conversion.ApplyValue(t.Context(), p.ConversionEvent, original, route, oldSink)
							converted, newErr := conversion.Event(t.Context(), event, route, newSink)
							if fmt.Sprint(oldErr) != fmt.Sprint(newErr) || !reflect.DeepEqual(oldSink.Issues(), newSink.Issues()) {
								t.Fatalf("conversion differs: %v / %v", oldErr, newErr)
							}
							if oldErr != nil {
								return
							}
							newValue, _ := p.EncodeValue(converted)
							if oldValue != newValue {
								t.Fatalf("semantic boundary differs\n%s\n%s", oldValue.Bytes(), newValue.Bytes())
							}
							unchanged, _ := p.EncodeValue(event)
							if unchanged != original {
								t.Fatal("conversion mutated source")
							}
							var oldEvent p.Event
							if err := oldValue.Decode(&oldEvent); err != nil {
								t.Fatal(err)
							}
							oldFrames, oldErr := generic.EncodeFrames(t.Context(), oldEvent, before)
							newFrames, newErr := typed.EncodeFrames(t.Context(), converted, after)
							retained, _ := p.EncodeValue(converted)
							if retained != newValue {
								t.Fatal("typed encoder mutated caller-owned event")
							}
							if fmt.Sprint(oldErr) != fmt.Sprint(newErr) || !reflect.DeepEqual(oldFrames, newFrames) {
								t.Fatalf("wire boundary differs\n%v %v\n%v %v", oldFrames, oldErr, newFrames, newErr)
							}
							if oldErr != nil {
								return
							}
						}
					}
				})
			}
		}
	}
}
