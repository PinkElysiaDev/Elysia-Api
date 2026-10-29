package builtin

import (
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// This candidate has no production registration or activation samples. The old
// codec is only the test oracle; the package compiler has no installed modules.
func TestChatPackageDraftMigration(t *testing.T) {
	legacyRaw, err := os.ReadFile("definitions/openai-chat-completions.json")
	if err != nil {
		t.Fatal(err)
	}
	legacyValue, err := p.ParseValue(legacyRaw)
	if err != nil {
		t.Fatal(err)
	}
	var definition p.Definition
	if err := legacyValue.Decode(&definition); err != nil {
		t.Fatal(err)
	}
	legacyCompiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, issues := legacyCompiler.Compile(legacyRaw)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	draftRaw, err := os.ReadFile("packages/openai-chat.draft.json")
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, issues := compiler.CompilePackage(draftRaw)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	responsePathGap := packageMigrationGap{
		reason: "legacy response message provenance inserts a synthetic /0; capture_native preserves the actual wire location",
		paths:  []string{"/content/0/native/source/path", "/content/0/source/path"},
	}
	gaps := map[string]packageMigrationGap{
		"text-decode-response":                   responsePathGap,
		"reasoning-response-decode":              responsePathGap,
		"cache-creation-decode-response":         responsePathGap,
		"cache-creation-missing-decode-response": responsePathGap,
		"tool-decode-response": {reason: responsePathGap.reason, paths: []string{
			"/content/0/children/0/native/source/path", "/content/0/children/0/source/path",
			"/content/0/native/source/path", "/content/0/source/path",
		}},
		"document-decode": {reason: "data URI needs a generic no-transcoding container parser", paths: []string{
			"/content/0/children/0/payload/data", "/content/0/children/0/payload/mime",
		}},
	}
	for _, sample := range definition.Samples {
		if sample.Direction == p.DecodeEvent || sample.Direction == p.EncodeEvent {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, gaps[sample.ID])
		})
		if sample.Direction == p.EncodeRequest || sample.Direction == p.EncodeResponse {
			t.Run("without-root-native/"+sample.ID, func(t *testing.T) {
				fields, err := sample.Input.ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				delete(fields, "native")
				sample.Input, err = p.EncodeValue(fields)
				if err != nil {
					t.Fatal(err)
				}
				options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
				want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
				got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			})
		}
	}
	for _, fixture := range []struct {
		name, wire string
		direction  p.Direction
		gap        packageMigrationGap
	}{
		{"request-fields", `{"model":"m","messages":[{"role":"user","content":"hi","name":"caller"}],"max_tokens":64,"temperature":0.25,"top_p":0.8,"stop":["END"],"stream":true,"parallel_tool_calls":false,"seed":123,"frequency_penalty":0,"presence_penalty":0,"metadata":{"n":9007199254740993},"store":false,"user":"u","response_format":{"type":"json_object"},"reasoning_effort":"high","n":1,"logprobs":true,"top_logprobs":2,"logit_bias":{"1":1},"stream_options":{"include_usage":true,"future":false},"future":{"n":1e3}}`, p.DecodeRequest, packageMigrationGap{}},
		{"tool-result-name", `{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"type":"function","id":"c","function":{"name":"lookup","arguments":"{}","future":1}}]},{"role":"tool","tool_call_id":"c","content":"done"}],"tools":[{"type":"function","future":1,"function":{"name":"lookup","description":"search","parameters":{"type":"object"},"strict":false,"future":2}}]}`, p.DecodeRequest, packageMigrationGap{}},
		{"image-extensions", `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image.png","detail":"high","future":2},"future":1}]}]}`, p.DecodeRequest, packageMigrationGap{}},
		{"usage-details", `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"service_tier":"default","system_fingerprint":null,"usage":{"prompt_tokens":9007199254740993,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":15,"text_tokens":10,"image_tokens":2,"audio_tokens":3},"completion_tokens_details":{"reasoning_tokens":1,"text_tokens":1,"image_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}`, p.DecodeResponse, responsePathGap},
	} {
		t.Run("boundary/"+fixture.name, func(t *testing.T) {
			sample := p.Sample{Direction: fixture.direction, Input: testValue(t, fixture.wire)}
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, fixture.gap)
		})
	}
}
