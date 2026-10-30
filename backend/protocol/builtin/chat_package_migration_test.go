package builtin

import (
	"bytes"
	"errors"
	"os"
	"strings"
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
	checkChatRequestOwners(t, legacy, draft)
	checkChatReasoningParameters(t, legacy, draft)
	checkPackageTrackingUsers(t, legacy, draft)
	checkPackageMediaURIs(t, legacy, draft, definition)
	wire := testValue(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`)
	for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
		t.Run("named-tool-choice/"+string(direction), func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			sample := p.Sample{Direction: direction, Input: wire}
			if direction == p.EncodeRequest {
				request, err := legacy.DecodeRequest(t.Context(), wire.Bytes(), options)
				if err != nil {
					t.Fatal(err)
				}
				request.Native = nil
				sample.Input, err = p.EncodeValue(request)
				if err != nil {
					t.Fatal(err)
				}
			}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
}

func checkChatReasoningParameters(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	for _, fixture := range []struct{ name, reasoning, effort, rejection string }{
		{"absent", "", "", ""},
		{"legacy", "", `"high"`, ""},
		{"legacy-null", "", `null`, ""},
		{"legacy-unknown-value", "", `"future"`, ""},
		{"empty-reasoning", `{}`, `"high"`, ""},
		{"null-reasoning", `null`, `"high"`, ""},
		{"typed", `{"effort":"high","effortScope":"reasoning"}`, "", ""},
		{"typed-null", `{"effort":null}`, "", ""},
		{"equal-effort-conflict", `{"effort":"high","effortScope":"reasoning"}`, `"high"`, "legacy-effort-conflict"},
		{"legacy-null-conflict", `{"effort":"high","effortScope":"reasoning"}`, `null`, "legacy-effort-conflict"},
		{"typed-null-conflict", `{"effort":null}`, `null`, "legacy-effort-conflict"},
		{"null-budget-before-conflict", `{"effort":"high","effortScope":"reasoning","budget":null}`, `"high"`, "unresolved-reasoning-controls"},
		{"null-summary", `{"summary":null}`, `"high"`, "unresolved-reasoning-controls"},
	} {
		t.Run("reasoning-parameters/"+fixture.name, func(t *testing.T) {
			request := &p.Request{SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{}}
			if fixture.reasoning != "" {
				if err := testValue(t, fixture.reasoning).Decode(&request.Reasoning); err != nil {
					t.Fatal(err)
				}
			}
			if fixture.effort != "" {
				request.Parameters["reasoning_effort"] = testValue(t, fixture.effort)
			}
			// This comparison exercises public typed encoding. Raw Execute has its
			// own preflight boundary and is not covered by these error assertions.
			want, wantErr := legacy.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			candidate := request.Clone()
			candidate.SchemaVersion = p.ProtocolCoreVersion
			got, gotErr := draft.EncodeRequest(t.Context(), candidate, p.EvaluationContext{})
			if fixture.rejection == "" {
				if wantErr != nil {
					t.Fatal("legacy reasoning fixture must encode", wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
				return
			}
			code, path, reason := p.InvalidInput, "/reasoning/effort", "conflicting typed and legacy effort"
			if fixture.rejection == "unresolved-reasoning-controls" {
				code, path, reason = p.UnsupportedCapability, "/reasoning", "Chat cannot encode unresolved budget or summary controls"
			}
			var oldFailure, newFailure *p.ConversionError
			if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 || oldFailure.Issues[0].Code != code || oldFailure.Issues[0].Stage != "request.validation" || oldFailure.Issues[0].Path != path || oldFailure.Issues[0].Reason != reason ||
				!errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 || newFailure.Issues[0].Code != p.ConversionRejected || newFailure.Issues[0].Stage != "mapping.effects" || newFailure.Issues[0].Path != path || newFailure.Issues[0].Reason != reason || newFailure.Issues[0].RuleID != fixture.rejection {
				t.Fatal("reasoning rejection or precedence changed", wantErr, gotErr)
			}
			t.Log("NOT CLOSED: request validation and declarative effect diagnostics differ", migrationError(wantErr), migrationError(gotErr))
		})
	}
}

func checkChatRequestOwners(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct{ name, options string }{
		{"known-and-unknown", `{"mode":"implicit","ttl":"1h","future":{"n":9007199254740993,"empty":null}}`},
		{"unknown-only", `{"future":null}`},
		{"empty", `{}`},
		{"null", `null`},
	} {
		t.Run("request-owner/"+fixture.name, func(t *testing.T) {
			raw := `{"model":"m","messages":[{"role":"user","content":"hi"}],"future_root":{},"prompt_cache_options":` + fixture.options + `}`
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, raw)}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			if wantErr != nil {
				t.Fatal(wantErr)
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			original, err := legacy.DecodeRequest(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := draft.DecodeRequest(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			edits := []string{"native", "without-root-native"}
			if fixture.name == "known-and-unknown" {
				edits = append(edits, "cache-mode", "remove-cache-owner")
			}
			for _, edit := range edits {
				t.Run(edit, func(t *testing.T) {
					oldRequest, newRequest := original.Clone(), decoded.Clone()
					for _, request := range []*p.Request{oldRequest, newRequest} {
						switch edit {
						case "without-root-native":
							request.Native = nil
						case "cache-mode":
							for index := range request.Cache {
								if request.Cache[index].Kind == "mode" {
									request.Cache[index].Value = p.StringValue("explicit")
								}
							}
						case "remove-cache-owner":
							owner, err := request.Parameters["wire:openai_chat"].ReadObject()
							if err != nil {
								t.Fatal(err)
							}
							delete(owner, "prompt_cache_options")
							request.Parameters["wire:openai_chat"], err = p.EncodeValue(owner)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					want, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
					got, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
					if wantErr != nil {
						t.Fatal(wantErr)
					}
					checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
					if edit == "native" && !bytes.Equal(got, []byte(raw)) {
						t.Fatal("unchanged request lost its original bytes")
					}
				})
			}
		})
	}
	checkChatOwnerMerge(t, legacy, draft, options)
}

func checkChatOwnerMerge(t *testing.T, legacy, draft *p.Compiled, options p.EvaluationContext) {
	t.Helper()
	base, err := legacy.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object","properties":{"answer":{"type":"string"}}}}}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	base.Native = nil
	if _, err := legacy.EncodeRequest(t.Context(), base, options); err != nil {
		t.Fatal("legacy JSON Schema fixture must encode without Native", err)
	}
	for _, fixture := range []struct{ name, owner, conflict string }{
		{"empty", `{}`, ""},
		{"nested-union", `{"response_format":{"json_schema":{"schema":{"description":"synthetic"}}}}`, ""},
		{"equal-leaf-conflict", `{"response_format":{"json_schema":{"schema":{"type":"object"}}}}`, "/response_format/json_schema/schema/type"},
		{"null-owner", `null`, ""},
	} {
		t.Run("request-owner-merge/"+fixture.name, func(t *testing.T) {
			oldRequest := base.Clone()
			oldRequest.Parameters["wire:openai_chat"] = testValue(t, fixture.owner)
			newRequest := oldRequest.Clone()
			newRequest.SchemaVersion = p.ProtocolCoreVersion
			want, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
			got, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
			if fixture.conflict == "" && fixture.owner != "null" {
				if wantErr != nil {
					t.Fatal(wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
				return
			}
			var oldFailure, newFailure *p.ConversionError
			if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 || !errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 || newFailure.Issues[0].Code != p.InvalidInput || newFailure.Issues[0].Stage != "evaluate" {
				t.Fatal("owner rejection changed or lost classification", wantErr, gotErr)
			}
			if fixture.conflict != "" {
				var conflict *p.ObjectMergeConflictError
				if oldFailure.Issues[0].Code != p.UnsupportedCapability || oldFailure.Issues[0].Stage != "wire" || oldFailure.Issues[0].Path != fixture.conflict || !errors.As(gotErr, &conflict) || conflict.Path != fixture.conflict {
					t.Fatal("nested leaf collision lost its structured cause", wantErr, gotErr)
				}
			} else if oldFailure.Issues[0].Code != p.InvalidInput || oldFailure.Issues[0].Stage != "runtime" || !strings.Contains(wantErr.Error(), "expected JSON object") || !strings.Contains(gotErr.Error(), "expected JSON object") || errors.Unwrap(gotErr) == nil {
				t.Fatal("null owner became absent or lost its cause", wantErr, gotErr)
			}
			t.Log("NOT CLOSED: codec wire/runtime diagnostics and DSL evaluate diagnostics differ", wantErr, gotErr)
		})
	}
}
