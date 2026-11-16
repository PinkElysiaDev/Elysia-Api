package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatOwnedExtensionsFollowCurrentCore(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct {
		name, wire, expected string
		edit                 func(*p.Request)
	}{
		{"tool", `{"model":"m","messages":[],"tools":[{"type":"function","vendor":null,"function":{"name":"lookup","parameters":{},"future":9007199254740993}}]}`,
			`{"model":"m","messages":[],"tools":[{"type":"function","vendor":null,"function":{"name":"renamed","parameters":{},"future":9007199254740993}}]}`,
			func(request *p.Request) { request.Tools[0].Name = p.StringValue("renamed") }},
		{"image", `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","future":false,"image_url":{"url":"https://example.invalid/a","future":9007199254740993}}]}]}`,
			`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","future":false,"image_url":{"url":"https://example.invalid/b","future":9007199254740993}}]}]}`,
			func(request *p.Request) {
				request.Content[0].Children[0].Payload = testValue(t, `{"url":"https://example.invalid/b"}`)
			}},
		{"tool-call", `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"type":"function","id":"c","future":null,"function":{"name":"lookup","arguments":"{}","future":9007199254740993}}]},{"role":"tool","tool_call_id":"c","name":"lookup","content":"ok"}]}`,
			`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"type":"function","id":"c","future":null,"function":{"name":"lookup","arguments":"{\"changed\":true}","future":9007199254740993}}]},{"role":"tool","tool_call_id":"c","name":"lookup","content":"ok"}]}`,
			func(request *p.Request) {
				request.Content[0].Children[0].Input.Value = testValue(t, `{"changed":true}`)
			}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			fixture.edit(request)
			before, _ := p.EncodeValue(request)
			wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, fixture.expected)
			if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{Responses, Anthropic, Gemini} {
				target := testCompiled(t, name)
				if _, err := target.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{})); err == nil {
					t.Fatal("foreign target accepted Chat's unknown owner", name)
				}
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("encoding mutated the caller's extension or current core")
			}
		})
	}
}

func TestChatOwnedExtensionsRejectMappedCollisions(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct {
		name, wire, extension, path string
	}{
		{"tool", `{"model":"m","messages":[],"tools":[{"type":"function","function":{"name":"lookup","parameters":{}}}]}`, `{"function":{"name":"lookup"}}`, "/tools/function/name"},
		{"image", `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/a"}}]}]}`, `{"image_url":{"url":"https://example.invalid/b"}}`, "/content/image_url/url"},
		{"tool-call", `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"type":"function","id":"c","function":{"name":"lookup","arguments":"{}"}}]}]}`, `{"function":{"name":"changed"}}`, "/tool_calls/function/name"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			owner := p.Object{"wire:openai_chat": testValue(t, fixture.extension)}
			if fixture.name == "tool" {
				request.Tools[0].Options["wire:openai_chat"] = owner["wire:openai_chat"]
			} else {
				request.Content[0].Children[0].Attributes = owner
			}
			_, err = compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			var failure *p.ConversionError
			var conflict *p.ObjectMergeConflictError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.UnsupportedCapability || failure.Issues[0].Path != fixture.path || !errors.As(err, &conflict) {
				t.Fatal("owned extension overrode mapped state or lost its conflict path", err)
			}
		})
	}
}
