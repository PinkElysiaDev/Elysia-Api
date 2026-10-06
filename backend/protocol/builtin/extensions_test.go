package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestNestedMediaExtensionsPreserveEditsAndRejectCrossProtocol(t *testing.T) {
	for _, fixture := range []struct{ source, target, body string }{
		{Chat, Responses, `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.org/old.png","vendor":{"exact":9007199254740993}}}]}]}`},
		{Chat, Gemini, `{"model":"m","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"YQ==","format":"wav","vendor":false}}]}]}`},
		{Chat, Responses, `{"model":"m","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"data:application/pdf;base64,YQ==","vendor":null}}]}]}`},
		{Anthropic, Chat, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"YQ==","media_type":"image/png","vendor":[]}}]}]}`},
		{Gemini, Anthropic, `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"YQ==","vendor":{}}}]}]}`},
	} {
		t.Run(fixture.source+fixture.target, func(t *testing.T) {
			source, target := testCompiled(t, fixture.source), testCompiled(t, fixture.target)
			request, err := source.DecodeRequest(t.Context(), []byte(fixture.body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			request.Model = p.StringValue("changed")
			block := &request.Content[0].Children[0]
			if len(block.Attributes) == 0 {
				t.Fatal("nested extension has no semantic evidence")
			}
			media, err := block.Payload.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			if !media["url"].IsZero() {
				media["url"] = p.StringValue("https://example.org/new.png")
			} else {
				media["data"] = p.StringValue("Yg==")
			}
			block.Payload = object(media)
			encoded, err := source.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"vendor"`) || strings.Contains(string(encoded), "old.png") || strings.Contains(string(encoded), "YQ==") {
				t.Fatalf("extension or semantic edit lost: %s", encoded)
			}
			if _, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
				t.Fatal("nested extension disappeared across protocol boundary")
			}
		})
	}
}

func TestChatToolEnvelopeExtensionSurvivesRename(t *testing.T) {
	compiled := testCompiled(t, Chat)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","vendor":false,"function":{"name":"old","parameters":{},"inner":9007199254740993}}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Tools[0].Name = p.StringValue("new")
	encoded, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := testValue(t, string(encoded)).ReadObject()
	sameJSON(t, fields["tools"].Bytes(), `[{"type":"function","vendor":false,"function":{"name":"new","parameters":{},"inner":9007199254740993}}]`)
	if _, err := testCompiled(t, Responses).EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
		t.Fatal("tool envelope extension was dropped")
	}
}
