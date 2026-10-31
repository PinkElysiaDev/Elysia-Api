package builtin

import (
	"bytes"
	"errors"
	"os"
	"strings"
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
	draft := compileBuiltinPackage(t, "google-generate-content")
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
	for _, edit := range []string{"unchanged", "current-text", "removed-extension"} {
		t.Run("system-wrapper-render/"+edit, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			request, err := legacy.DecodeRequest(t.Context(), []byte(`{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}],"vendor":{"n":9007199254740993,"empty":null}},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`), options)
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			switch edit {
			case "current-text":
				request.Content[0].Children[0].Payload = p.StringValue("edited prefix")
			case "removed-extension":
				delete(request.Parameters, "wire:gemini")
			}
			input, err := p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			compareGeminiMigration(t, legacy, draft, p.Sample{Direction: p.EncodeRequest, Input: input}, options)
		})
	}
	checkGeminiReasoningMigration(t, legacy, draft)
	checkGeminiCacheRemainder(t, legacy, draft)
	checkGeminiGenerationOwner(t, legacy, draft)
}

func checkGeminiGenerationOwner(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	base := &p.Request{SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{
		"temperature": testValue(t, `0.25`), "stop": testValue(t, `[]`),
		"gemini_response_schema": testValue(t, `{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}}
	for _, fixture := range []struct{ name, owner, conflict string }{
		{"generation-union", `{"generationConfig":{"future":{"n":9007199254740993,"empty":null,"spelling":1e2}}}`, ""},
		{"nested-schema-union", `{"generationConfig":{"responseSchema":{"description":"synthetic","properties":{"answer":{"description":"current answer"}}}}}`, ""},
		{"equal-leaf-conflict", `{"generationConfig":{"temperature":0.25}}`, "/generationConfig/temperature"},
		{"null-leaf-conflict", `{"generationConfig":{"temperature":null}}`, "/generationConfig/temperature"},
		{"equal-array-conflict", `{"generationConfig":{"stopSequences":[]}}`, "/generationConfig/stopSequences"},
		{"object-null-conflict", `{"generationConfig":{"responseSchema":null}}`, "/generationConfig/responseSchema"},
	} {
		t.Run("generation-owner/"+fixture.name, func(t *testing.T) {
			request := base.Clone()
			request.Parameters["wire:gemini"] = testValue(t, fixture.owner)
			candidate := request.Clone()
			candidate.SchemaVersion = p.ProtocolCoreVersion
			before, err := p.EncodeValue(candidate)
			if err != nil {
				t.Fatal(err)
			}
			want, wantErr := legacy.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			got, gotErr := draft.EncodeRequest(t.Context(), candidate, p.EvaluationContext{})
			if fixture.conflict == "" {
				if wantErr != nil {
					t.Fatal("legacy generation owner fixture must encode", wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			} else {
				var oldFailure, newFailure *p.ConversionError
				var conflict *p.ObjectMergeConflictError
				if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 || oldFailure.Issues[0].Code != p.UnsupportedCapability || oldFailure.Issues[0].Stage != "wire" || oldFailure.Issues[0].Path != fixture.conflict ||
					!errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 || newFailure.Issues[0] != oldFailure.Issues[0] || !errors.As(gotErr, &conflict) || conflict.Path != fixture.conflict {
					t.Fatal("generation owner collision changed or lost its structured path", migrationError(wantErr), migrationError(gotErr))
				}
			}
			after, err := p.EncodeValue(candidate)
			if err != nil || before != after {
				t.Fatal("generation owner merge changed the caller's request", err)
			}
		})
	}
}

func checkGeminiCacheRemainder(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct{ name, cache, extra string }{
		{"mixed", `{"future":{"items":[null,1e2,{}]}}`, `,"future_root":{"n":9007199254740993,"empty":null},"generationConfig":{"temperature":0.25,"future_gen":{"enabled":false}}`},
		{"unknown-null", `{"future":null}`, ""},
		{"empty", `{}`, ""},
		{"null", `null`, ""},
	} {
		t.Run("cache-remainder/"+fixture.name, func(t *testing.T) {
			raw := `{"contents":[],"prompt_cache_options":` + fixture.cache + fixture.extra + `}`
			wire := testValue(t, raw)
			request, err := legacy.DecodeRequest(t.Context(), wire.Bytes(), options)
			if err != nil {
				t.Fatal("valid remainder fixture rejected by baseline", err)
			}
			t.Run("decode", func(t *testing.T) {
				compareGeminiMigration(t, legacy, draft, p.Sample{Direction: p.DecodeRequest, Input: wire}, options)
			})
			if fixture.name == "mixed" {
				t.Run("native", func(t *testing.T) {
					decoded, err := draft.DecodeRequest(t.Context(), wire.Bytes(), options)
					if err != nil {
						t.Fatal(err)
					}
					want, wantErr := legacy.EncodeRequest(t.Context(), request, options)
					got, gotErr := draft.EncodeRequest(t.Context(), decoded, options)
					if wantErr != nil {
						t.Fatal(wantErr)
					}
					checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
					if !bytes.Equal(got, wire.Bytes()) {
						t.Fatal("unchanged remainder lost original bytes")
					}
				})
			}
			request.Native = nil
			t.Run("without-root-native", func(t *testing.T) {
				want, wantErr := legacy.EncodeRequest(t.Context(), request, options)
				if wantErr != nil {
					t.Fatal("legacy remainder rendering failed", wantErr)
				}
				current := request.Clone()
				current.SchemaVersion = p.ProtocolCoreVersion
				got, gotErr := draft.EncodeRequest(t.Context(), current, options)
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			})
		})
	}
	t.Run("cache-remainder/system-owner", func(t *testing.T) {
		// Reuse the old core for encoding: the existing system provenance gap is
		// unrelated to whether both unknown owners survive semantic rendering.
		request, err := legacy.DecodeRequest(t.Context(), []byte(`{"systemInstruction":{"role":"user","parts":[{"text":"prefix"}],"vendor":{"n":9007199254740993}},"contents":[],"prompt_cache_options":{"future":null}}`), options)
		if err != nil {
			t.Fatal(err)
		}
		request.Native = nil
		want, wantErr := legacy.EncodeRequest(t.Context(), request, options)
		if wantErr != nil {
			t.Fatal("legacy system/owner fixture failed", wantErr)
		}
		current := request.Clone()
		current.SchemaVersion = p.ProtocolCoreVersion
		got, gotErr := draft.EncodeRequest(t.Context(), current, options)
		checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
	})
	checkGeminiOwnerBucket(t, legacy, draft, options)
}

func checkGeminiOwnerBucket(t *testing.T, legacy, draft *p.Compiled, options p.EvaluationContext) {
	t.Helper()
	base, err := legacy.DecodeRequest(t.Context(), []byte(`{"contents":[]}`), options)
	if err != nil {
		t.Fatal(err)
	}
	base.Native = nil
	for _, owner := range []string{"", `{}`, `null`} {
		name := owner
		if name == "" {
			name = "absent"
		}
		t.Run("cache-remainder/owner-"+name, func(t *testing.T) {
			request := base.Clone()
			if owner != "" {
				request.Parameters = p.Object{"wire:gemini": testValue(t, owner)}
			}
			current := request.Clone()
			current.SchemaVersion = p.ProtocolCoreVersion
			want, wantErr := legacy.EncodeRequest(t.Context(), request, options)
			got, gotErr := draft.EncodeRequest(t.Context(), current, options)
			if owner != "null" {
				if wantErr != nil {
					t.Fatal(wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
				return
			}
			var oldFailure, newFailure *p.ConversionError
			if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 || oldFailure.Issues[0].Code != p.InvalidInput || oldFailure.Issues[0].Stage != "runtime" || !strings.Contains(wantErr.Error(), "expected JSON object") ||
				!errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 || newFailure.Issues[0].Code != p.InvalidInput || newFailure.Issues[0].Stage != "evaluate" ||
				!strings.HasSuffix(newFailure.Issues[0].Path, "/@encode-system-envelope/body/source") || !strings.Contains(gotErr.Error(), "JSON pointer traverses a scalar") || errors.Unwrap(gotErr) == nil {
				t.Fatal("null owner acceptance or existing rejection point changed", wantErr, gotErr)
			}
			t.Log("NOT CLOSED: existing null owner rejection differs; draft rejects the system-extension child read before the root guard", wantErr, gotErr)
		})
	}
}

func checkGeminiReasoningMigration(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct{ name, config string }{
		{"level-with-remainder", `{"thinkingLevel":"medium","includeThoughts":true,"future":{"n":9007199254740993}}`},
		{"automatic-budget", `{"thinkingBudget":-1,"includeThoughts":false}`},
		{"disabled-budget", `{"thinkingBudget":0}`},
		{"manual-budget", `{"thinkingBudget":4096}`},
		{"include-only", `{"includeThoughts":true}`},
		{"empty", `{}`},
		{"null", `null`},
		{"unknown-only", `{"future":null}`},
		{"null-level", `{"thinkingLevel":null}`},
		{"null-budget", `{"thinkingBudget":null}`},
		{"null-level-manual-budget", `{"thinkingLevel":null,"thinkingBudget":2048}`},
	} {
		wire := testValue(t, `{"contents":[],"generationConfig":{"thinkingConfig":`+fixture.config+`}}`)
		request, err := legacy.DecodeRequest(t.Context(), wire.Bytes(), options)
		if err != nil {
			t.Fatal("valid reasoning fixture rejected by baseline", fixture.name, err)
		}
		request.Native = nil
		core, err := p.EncodeValue(request)
		if err != nil {
			t.Fatal(err)
		}
		for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
			t.Run("thinking/"+fixture.name+"/"+string(direction), func(t *testing.T) {
				input := wire
				if direction == p.EncodeRequest {
					input = core
				}
				compareGeminiMigration(t, legacy, draft, p.Sample{Direction: direction, Input: input}, options)
			})
		}
	}
	for _, edit := range []string{"current-level", "removed-controls"} {
		t.Run("thinking/"+edit, func(t *testing.T) {
			request, err := legacy.DecodeRequest(t.Context(), []byte(`{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","future":{"n":9007199254740993}}}}`), options)
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			if edit == "current-level" {
				request.Reasoning.Effort = p.StringValue("low")
			} else {
				request.Reasoning = &p.ReasoningIntent{}
			}
			input, err := p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			compareGeminiMigration(t, legacy, draft, p.Sample{Direction: p.EncodeRequest, Input: input}, options)
		})
	}
	for _, fixture := range []struct{ name, config string }{
		{"scalar", `false`},
		{"level-and-budget", `{"thinkingLevel":"high","thinkingBudget":1024}`},
		{"null-include", `{"includeThoughts":null}`},
		{"unsupported-level", `{"thinkingLevel":"xhigh"}`},
		{"negative-budget", `{"thinkingBudget":-2}`},
	} {
		t.Run("thinking/reject-"+fixture.name, func(t *testing.T) {
			wire := testValue(t, `{"contents":[],"generationConfig":{"thinkingConfig":`+fixture.config+`}}`)
			compareGeminiMigration(t, legacy, draft, p.Sample{Direction: p.DecodeRequest, Input: wire}, options)
		})
	}
	for _, fixture := range []struct {
		name       string
		reasoning  *p.ReasoningIntent
		parameters p.Object
	}{
		{"canonical-conflict", &p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue("high"), EffortScope: "reasoning"}, p.Object{"gemini_thinking": testValue(t, `{"thinkingLevel":"low"}`)}},
		{"canonical-level-and-budget", &p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue("high"), EffortScope: "reasoning", Budget: testValue(t, `1024`)}, nil},
	} {
		t.Run("thinking/reject-"+fixture.name, func(t *testing.T) {
			request := p.Request{SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"), Content: []p.Node{}, Reasoning: fixture.reasoning, Parameters: fixture.parameters}
			input, err := p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			compareGeminiMigration(t, legacy, draft, p.Sample{Direction: p.EncodeRequest, Input: input}, options)
		})
	}
}

var geminiMigrationGaps = map[string]packageMigrationGap{
	"TestGeminiPackageDraftMigration/text-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/history-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
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
		reason:                   "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/tool-decode-response": {
		reason:                   "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/cache-policy-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/image-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/audio-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/video-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/document-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/hosted-tool-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/reasoning-history-decode": {
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
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
		reason:                   "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		isApprovedRepresentation: true,
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
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
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
		reason:                   "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		isApprovedRepresentation: true,
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
		reason:                   "legacy Gemini part provenance uses /content; capture_native preserves the original /parts location",
		isApprovedRepresentation: true,
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
		reason:                   "legacy system parts use the synthetic /system path; capture_native preserves /systemInstruction/parts",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/response-omitted-role": {
		reason:                   "legacy response content is wrapped and re-encoded; capture_native preserves the original location and value",
		isApprovedRepresentation: true,
		paths: []string{
			"/content/0/children/0/native/source/path",
			"/content/0/children/0/source/path",
			"/content/0/native/source/path",
			"/content/0/native/value/role",
			"/content/0/source/path",
		},
	},
	"TestGeminiPackageDraftMigration/boundary/mixed-tool-envelope": {
		reason:                   "legacy hosted snapshot removes functionDeclarations; capture_native preserves the original object",
		isApprovedRepresentation: true,
		paths: []string{
			"/tools/1/native/value/functionDeclarations",
		},
	},
	"TestGeminiPackageDraftMigration/thinking/reject-scalar": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-level-and-budget": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-null-include": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-unsupported-level": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-negative-budget": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-canonical-conflict": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
	"TestGeminiPackageDraftMigration/thinking/reject-canonical-level-and-budget": {
		reason:    "Gemini reasoning rejection retains acceptance but has different wire/mapping diagnostics",
		rejection: true,
	},
}

func compareGeminiMigration(t *testing.T, legacy, draft *p.Compiled, sample p.Sample, options p.EvaluationContext) {
	t.Helper()
	want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
	got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
	checkPackageMigration(t, want, got, wantErr, gotErr, geminiMigrationGaps[t.Name()])
	if geminiMigrationGaps[t.Name()].isApprovedRepresentation {
		checkMigrationReencoding(t, legacy, draft, sample, options)
	}
}
