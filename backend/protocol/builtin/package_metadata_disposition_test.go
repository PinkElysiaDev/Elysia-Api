package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageMetadataDispositionChecksBeforeOmission(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-chat")
	if _, err := source.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","created":1,"choices":[],"service_tier":7}`), fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("source accepted an invalid service tier before target projection")
	}
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			route := conversion.VerificationRoute(compiled.Identity(), compiled.Identity(), p.HTTPJSON)
			options, err := (p.EvaluationContext{}).WithOperation("metadata-test", 1)
			if err != nil {
				t.Fatal(err)
			}
			route = route.WithOperation(options)
			for _, sample := range []struct {
				name  string
				field p.ResponseMetadata
			}{
				{"unknown-empty-field", p.ResponseMetadata{Codec: "future-provider", Location: "response", Name: "future_state", Value: testValue(t, "null")}},
				{"ambiguous-namespace", p.ResponseMetadata{Codec: "openai-chat|response", Location: "", Name: "service_tier", Value: p.StringValue("standard")}},
			} {
				sample.field.Path = "/provider/" + sample.field.Name
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Status: p.StringValue("completed"), Metadata: []p.ResponseMetadata{sample.field}}
				_, err := conversion.Response(t.Context(), response, route, nil)
				var failure *p.ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != sample.field.Path {
					t.Fatal("metadata guard lost the original field location", sample.name, err)
				}
				if failure.Issues[0].Code != p.ConversionRejected {
					t.Fatal("metadata guard changed classification", sample.name, failure.Issues)
				}
			}
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Status: p.StringValue("completed"), Metadata: []p.ResponseMetadata{{
				Meaning: "service-tier", Codec: "future-provider", Location: "response", Name: "quality", Path: "/quality", Value: p.StringValue("standard"),
			}}}
			if _, err := conversion.Response(t.Context(), response, route, nil); err != nil {
				t.Fatal("declared metadata rejected", err)
			}
		})
	}
}
