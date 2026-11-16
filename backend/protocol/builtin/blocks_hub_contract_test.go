package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiSafetyUsesCanonicalSettings(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	categories := map[string]string{
		"HARM_CATEGORY_HARASSMENT": "harassment", "HARM_CATEGORY_HATE_SPEECH": "hate-speech",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT": "sexual-content", "HARM_CATEGORY_DANGEROUS_CONTENT": "dangerous-content",
		"HARM_CATEGORY_CIVIC_INTEGRITY": "civic-integrity",
	}
	thresholds := map[string]string{
		"OFF": "off", "BLOCK_NONE": "none", "BLOCK_LOW_AND_ABOVE": "low",
		"BLOCK_MEDIUM_AND_ABOVE": "medium", "BLOCK_ONLY_HIGH": "high", "HARM_BLOCK_THRESHOLD_UNSPECIFIED": "default",
	}
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	for wireCategory, category := range categories {
		for wireThreshold, threshold := range thresholds {
			body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"safetySettings":[{"category":"` + wireCategory + `","threshold":"` + wireThreshold + `"}]}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
			if err != nil {
				t.Fatal(err)
			}
			want := testValue(t, `[{"category":"`+category+`","threshold":"`+threshold+`"}]`)
			if !equivalentJSON(request.Parameters["safety"], want) || !request.Parameters["gemini_safety_settings"].IsZero() {
				t.Fatal("source wire settings were not mapped to the hub", request.Parameters)
			}
			request.Native = nil
			wire, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
		}
	}
	for _, settings := range []string{`[{"category":"FUTURE","threshold":"OFF"}]`, `[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"FUTURE"}]`, `[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF","vendor":false}]`} {
		body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"safetySettings":` + settings + `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
		if err != nil || !request.Parameters["safety"].IsZero() {
			t.Fatal("unknown settings acquired canonical authority", request, err)
		}
		request.Native = nil
		wire, err := compiled.EncodeRequest(t.Context(), request, options)
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		request.Parameters["safety"] = testValue(t, `[{"category":"harassment","threshold":"off"}]`)
		if _, err := compiled.EncodeRequest(t.Context(), request, options); err == nil {
			t.Fatal("unknown source settings overwrote canonical settings")
		}
	}
}

func TestAnthropicTrackingUsesCanonicalIdentity(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	for _, metadata := range []string{`null`, `{}`, `{"user_id":null}`, `{"user_id":"source"}`, `{"user_id":"source","vendor":{"trace":1}}`} {
		body := `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"metadata":` + metadata + `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		if !request.Parameters["anthropic_metadata"].IsZero() {
			t.Fatal("source retained retired tracking wrapper")
		}
		if strings.Contains(metadata, `"user_id"`) && request.Parameters["user"].IsZero() {
			t.Fatal("source identity did not reach the hub")
		}
		if strings.Contains(request.Parameters["wire:claude"].JSONString(), "user_id") {
			t.Fatal("opaque source remainder duplicated the canonical identity")
		}
		request.Native = nil
		wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		if request.Parameters["user"].IsString("source") {
			request.Parameters["user"] = p.StringValue("edited")
			wire, err = compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, strings.Replace(body, `"source"`, `"edited"`, 1))
		}
	}
}
