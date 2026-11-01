package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageGeminiResultEnvelope(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	for _, fixture := range []struct {
		name, response, payload, status string
	}{
		{"mixed", `{"error":{"message":"partial"},"output":{"value":1},"trace":null}`, `{"error":{"message":"partial"},"output":{"value":1},"trace":null}`, `{"isError":true,"payloadForm":"result_envelope"}`},
		{"error-extra", `{"error":"failed","trace":1}`, `{"error":"failed","trace":1}`, `{"isError":true,"payloadForm":"result_envelope"}`},
		{"output-extra", `{"output":null,"trace":false}`, `{"output":null,"trace":false}`, `{"isError":false,"payloadForm":"result_envelope"}`},
		{"null-error", `{"error":null}`, `null`, `{"isError":true}`},
		{"ordinary-object", `{"value":1}`, `{"value":1}`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			wire := fmt.Sprintf(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call","name":"f","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call","name":"f","response":%s,"trace":{"id":"original"}}}]}]}`, fixture.response)
			request, err := compiled.DecodeRequest(t.Context(), []byte(wire), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			node := &request.Content[1].Children[0]
			if node.Payload != testValue(t, fixture.payload) || fixture.status == "" && !node.Status.IsZero() || fixture.status != "" && node.Status != testValue(t, fixture.status) {
				t.Fatal("result data and status were not separated", node)
			}
			request.Native, request.Content[1].Native, node.Native = nil, nil, nil
			node.CallID = p.StringValue("changed")
			request.Content[0].Children[0].CallID = p.StringValue("changed")
			encoded, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			var observed struct {
				Contents []struct {
					Parts []struct {
						FunctionResponse struct {
							ID              string
							Response, Trace p.Value
						}
					}
				}
			}
			value, err := p.ParseValue(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if err := value.Decode(&observed); err != nil {
				t.Fatal(err)
			}
			result := observed.Contents[1].Parts[0].FunctionResponse
			if result.ID != "changed" || result.Response != testValue(t, fixture.response) || result.Trace != testValue(t, `{"id":"original"}`) {
				t.Fatal("rebuilt result changed envelope or extension", string(encoded))
			}
		})
	}
}

func TestPackageResultEnvelopeDisposition(t *testing.T) {
	source := compileBuiltinPackage(t, "google-generate-content")
	for _, name := range []string{"google-generate-content", "anthropic", "openai-chat", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			request := toolResultRequest(t, `{"error":{"message":"partial"},"output":{"value":1},"trace":null}`, `{"isError":true,"payloadForm":"result_envelope"}`)
			original, _ := p.EncodeValue(request)
			policy := declaredRulePolicy(t, source, target, "tool-results", "compatible")
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			result, err := conversion.Request(t.Context(), request, route, sink)
			if err != nil {
				t.Fatal(err)
			}
			if name == "google-generate-content" {
				if result.Content[1].Payload != request.Content[1].Payload || result.Content[1].Status != request.Content[1].Status || len(sink.Issues()) != 0 {
					t.Fatal("native envelope was projected", result.Content[1], sink.Issues())
				}
			} else {
				want := string(request.Content[1].Payload.Bytes())
				if name != "anthropic" {
					want = "[Tool error] " + want
				}
				if !result.Content[1].Payload.IsString(want) || len(sink.Issues()) == 0 || sink.Issues()[0].Fidelity != "lossy_compatible" {
					t.Fatal("envelope data or loss declaration changed", result.Content[1], sink.Issues())
				}
			}
			if _, err := target.EncodeRequest(t.Context(), result, p.EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
			policy.Mode = "strict"
			strict, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := strict.Request(t.Context(), request, route, nil); (err != nil) != (name != "google-generate-content") {
				t.Fatal("strict envelope boundary changed", err)
			}
			policy.Mode, policy.Rules[0].Enabled = "compatible", false
			disabled, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			unresolved, err := disabled.Request(t.Context(), request, route, nil)
			if err == nil {
				_, err = target.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{})
			}
			if (err == nil) != (name == "google-generate-content") {
				t.Fatal("disabled envelope rule was implicitly repeated", err)
			}
			after, _ := p.EncodeValue(request)
			if after != original {
				t.Fatal("projection changed caller input")
			}
		})
	}
}

func TestPackageResultEnvelopeRejectsUnrepresentedState(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	for _, status := range []string{
		`{"isError":false,"payloadForm":"result_envelope"}`,
		`{"isError":true,"payloadForm":"result_envelope","future":1}`,
	} {
		request := toolResultRequest(t, `{"error":"failed","output":"partial"}`, status)
		if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
			t.Fatal("unrepresented envelope status was accepted", status)
		}
	}
	request := toolResultRequest(t, `{"error":"failed","output":"partial"}`, `{"isError":true,"payloadForm":"result_envelope"}`)
	request.Content[1].Attributes = p.Object{"wire:gemini": testValue(t, `{"functionResponse":{"response":{"error":"replacement"}}}`)}
	if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
		t.Fatal("extension replaced the semantic result owner")
	}
}
