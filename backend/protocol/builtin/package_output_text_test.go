package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageOutputTextChecksCurrentContent(t *testing.T) {
	source := compileBuiltinPackage(t, "openai-responses")
	response, err := source.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output_text":"A😀B","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A😀"},{"type":"refusal","refusal":"excluded"},{"type":"output_text","text":"B"}]}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	copyResponse := func() *p.Response {
		value, err := p.EncodeValue(response)
		if err != nil {
			t.Fatal(err)
		}
		var copy p.Response
		if err := value.Decode(&copy); err != nil {
			t.Fatal(err)
		}
		return &copy
	}
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			policy := declaredRulePolicy(t, target, source, "response-metadata", "compatible")
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := conversion.VerificationRoute(source.Identity(), target.Identity(), p.HTTPJSON)
			if _, err := conversion.Response(t.Context(), response, route, nil); err != nil {
				t.Fatal("matching segmented text was rejected", err)
			}
			for _, hasNative := range []bool{false, true} {
				edited := copyResponse()
				if !hasNative {
					edited.Native = nil
				}
				edited.Content[0].Children[0].Payload = p.StringValue("changed")
				_, err := conversion.Response(t.Context(), edited, route, nil)
				requireContextIssue(t, err, p.InvalidInput, "/output_text")
				if len(edited.Metadata) != len(response.Metadata) {
					t.Fatal("failed candidate changed its input metadata")
				}
			}
			// Terminal response snapshots use the same target declaration. Delta
			// events without a complete response are not compared with a prefix.
			policy = declaredRulePolicy(t, target, source, "event-metadata", "compatible")
			conversion, err = p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			event := p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: copyResponse()}
			event.Response.Content[0].Children[2].Payload = p.StringValue("changed")
			_, err = conversion.Event(t.Context(), event, route, nil)
			requireContextIssue(t, err, p.InvalidInput, "/output_text")
		})
	}
	response.Native = nil
	response.Content[0].Children[0].Payload = p.StringValue("changed")
	if _, err := source.EncodeResponseValue(t.Context(), response, p.EvaluationContext{}); err == nil {
		t.Fatal("direct encoding accepted conflicting output_text")
	}
}
