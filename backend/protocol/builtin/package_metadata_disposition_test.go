package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageMetadataDispositionChecksBeforeOmission(t *testing.T) {
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
				{"invalid-known-type", p.ResponseMetadata{Codec: Chat, Location: "response", Name: "service_tier", Value: testValue(t, "7")}},
				{"unknown-empty-field", p.ResponseMetadata{Codec: Chat, Location: "response", Name: "future_state", Value: testValue(t, "null")}},
				{"ambiguous-namespace", p.ResponseMetadata{Codec: "openai-chat|response", Location: "", Name: "service_tier", Value: p.StringValue("standard")}},
			} {
				sample.field.Path = "/provider/" + sample.field.Name
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Metadata: []p.ResponseMetadata{sample.field}}
				_, err := conversion.Response(t.Context(), response, route, nil)
				var failure *p.ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != sample.field.Path {
					t.Fatal("metadata guard lost the original field location", sample.name, err)
				}
				code := p.UnsupportedCapability
				if sample.name == "invalid-known-type" {
					code = p.InvalidInput
				}
				if failure.Issues[0].Code != code {
					t.Fatal("metadata guard changed classification", sample.name, failure.Issues)
				}
			}
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Metadata: []p.ResponseMetadata{{
				Codec: Chat, Location: "response", Name: "service_tier", Path: "/service_tier", Value: p.StringValue("standard"),
			}}}
			if _, err := conversion.Response(t.Context(), response, route, nil); err != nil {
				t.Fatal("declared metadata rejected", err)
			}
		})
	}
}
