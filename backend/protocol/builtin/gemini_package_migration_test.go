package builtin

import (
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The candidate executes without installed codecs and has no production loader
// entry. Its four JSON directions are compared with the shipped implementation.
func TestGeminiPackageDraftMigration(t *testing.T) {
	legacyRaw, err := os.ReadFile("definitions/google-generate-content.json")
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
	draftRaw, err := os.ReadFile("packages/google-generate-content.draft.json")
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
	for _, sample := range definition.Samples {
		if sample.Direction == p.DecodeEvent || sample.Direction == p.EncodeEvent {
			continue
		}
		options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
		t.Run(sample.ID, func(t *testing.T) {
			compareGeminiMigration(t, legacy, draft, sample, options)
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
				compareGeminiMigration(t, legacy, draft, sample, options)
			})
		}
	}
	for _, fixture := range []struct {
		name, scope string
		model       p.Value
	}{
		{"scope-model", "scoped-model", p.Value{}},
		{"empty-scope", "", p.Value{}},
		{"context-model", "scoped-model", p.StringValue("context-model")},
		{"context-empty", "scoped-model", p.StringValue("")},
		{"context-null", "scoped-model", nullValue()},
	} {
		t.Run("context/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: fixture.scope}, Values: p.Object{}}
			if !fixture.model.IsZero() {
				options.Values["model"] = fixture.model
			}
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, `{"model":"ignored-wire-model","contents":[]}`)}
			compareGeminiMigration(t, legacy, draft, sample, options)
		})
	}
	for _, fixture := range []struct {
		name, wire string
		direction  p.Direction
	}{
		{"idless-reuse", `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"explicit","name":"other","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"explicit","name":"other","response":{}}}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"\u006cookup","response":{"ok":true}}}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, p.DecodeRequest},
		{"idless-ambiguous", `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup","args":{}}},{"functionCall":{"id":"b","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{}}}]}]}`, p.DecodeRequest},
		{"system-wrapper", `{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}],"vendor":{"n":9007199254740993}},"contents":[]}`, p.DecodeRequest},
		{"response-omitted-role", `{"responseId":"r","candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`, p.DecodeResponse},
		{"mixed-tool-envelope", `{"contents":[],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}],"googleSearch":{}}]}`, p.DecodeRequest},
	} {
		t.Run("boundary/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			sample := p.Sample{Direction: fixture.direction, Input: testValue(t, fixture.wire)}
			compareGeminiMigration(t, legacy, draft, sample, options)
		})
	}
}

var geminiMigrationGaps = map[string]packageMigrationGap{
	"TestGeminiPackageDraftMigration/text-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/history-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/1/children/0/native/source/path",
			"/content/1/children/0/source/path",
			"/content/2/children/0/native/source/path",
			"/content/2/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/text-decode-response": {
		reason: "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/tool-decode-response": {
		reason: "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/cache-policy-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/image-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/audio-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/video-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/document-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/hosted-tool-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/reasoning-history-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/1/children/0/native/source/path",
			"/content/1/children/0/source/path",
			"/content/1/children/1/native/source/path",
			"/content/1/children/1/source/path",
			"/content/2/children/0/native/source/path",
			"/content/2/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/reasoning-response-decode": {
		reason: "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/children/1/native/source/path",
			"/content/0/children/1/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/signature-reasoning-history-decode": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/1/children/0/native/source/path",
			"/content/1/children/0/source/path",
			"/content/1/children/1/native/source/path",
			"/content/1/children/1/source/path",
			"/content/2/children/0/native/source/path",
			"/content/2/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/signature-reasoning-response-decode": {
		reason: "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/children/1/native/source/path",
			"/content/0/children/1/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/idless-reuse": {
		reason: "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/1/children/0/native/source/path",
			"/content/1/children/0/source/path",
			"/content/2/children/0/native/source/path",
			"/content/2/children/0/source/path",
			"/content/3/children/0/native/source/path",
			"/content/3/children/0/source/path",
			"/content/4/children/0/native/source/path",
			"/content/4/children/0/source/path",
			"/content/5/children/0/native/source/path",
			"/content/5/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/idless-ambiguous": {
		reason:    "ambiguous idless result remains rejected, but mapping diagnostics differ",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/boundary/system-wrapper": {
		reason: "legacy system parts use the synthetic /system path; capture_native preserves /systemInstruction/parts",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/response-omitted-role": {
		reason: "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value/role",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/mixed-tool-envelope": {
		reason: "legacy hosted snapshot removes functionDeclarations; capture_native preserves the original object",
		paths: []string{
			"/tools/1/native/value/functionDeclarations",
		},
	},
}

func compareGeminiMigration(t *testing.T, legacy, draft *p.Compiled, sample p.Sample, options p.EvaluationContext) {
	t.Helper()
	want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
	got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
	checkPackageMigration(t, want, got, wantErr, gotErr, geminiMigrationGaps[t.Name()])
}
