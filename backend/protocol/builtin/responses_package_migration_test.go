package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageMetadataWithoutNative(t *testing.T) {
	declaration := compileBuiltinPackage(t, "openai-responses")
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}]}`)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	legacy := shippedProjectionProtocol(t, Responses)
	oldResponse, err := legacy.DecodeResponse(t.Context(), raw, options)
	if err != nil {
		t.Fatal(err)
	}
	oldResponse.Native = nil
	wantWire, err := legacy.EncodeResponse(t.Context(), oldResponse, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, shouldPreserve := range []bool{true, false} {
		t.Run(fmt.Sprintf("native=%t", shouldPreserve), func(t *testing.T) {
			document, ok := declaration.Package()
			if !ok {
				t.Fatal("missing source package")
			}
			document.Mapping.Native.Preserve = shouldPreserve
			source, err := p.EncodeValue(document)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.CompilePackage(source.Bytes())
			if err := p.IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			response, err := compiled.DecodeResponse(t.Context(), raw, options)
			if err != nil {
				t.Fatal(err)
			}
			node := response.Content[0].Children[0]
			if node.Source.Path != "/output/0/content/0" || node.Source.Scope != options.Scope || (node.Native != nil) != shouldPreserve || len(node.Metadata) != 2 {
				t.Fatal("metadata provenance depends on native preservation", node)
			}
			for _, metadata := range node.Metadata {
				if metadata.Path != "/output/0/content/0/"+metadata.Name {
					t.Fatal("metadata lost its original location", metadata)
				}
			}
			response.Native = nil
			gotWire, err := compiled.EncodeResponse(t.Context(), response, options)
			if err != nil || !bytes.Equal(gotWire, wantWire) {
				t.Fatalf("metadata reconstruction changed: old=%s new=%s error=%v", wantWire, gotWire, err)
			}
		})
	}
}

// The four JSON directions are a standalone declaration candidate. The package
// compiler has no codec modules, and its samples are not activation evidence.
func TestResponsesPackageGroupedHistory(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Responses)
	draft := compileBuiltinPackage(t, "openai-responses")
	content := []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{
			{Kind: p.TextNode, Payload: p.StringValue("before")},
			{Kind: p.TextNode, Payload: p.StringValue("adjacent")},
			{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}},
			{Kind: p.TextNode, Payload: p.StringValue("after")},
		}},
		{Kind: p.MessageNode, Role: p.StringValue("assistant")},
	}
	for _, direction := range []p.Direction{p.EncodeRequest, p.EncodeResponse} {
		t.Run(string(direction), func(t *testing.T) {
			var oldWire, newWire []byte
			var oldErr, newErr error
			if direction == p.EncodeRequest {
				request := &p.Request{SchemaVersion: legacy.CoreVersion(), Model: p.StringValue("m"), Content: content}
				oldWire, oldErr = legacy.EncodeRequest(t.Context(), request, p.EvaluationContext{})
				request.SchemaVersion = draft.CoreVersion()
				newWire, newErr = draft.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			} else {
				response := &p.Response{SchemaVersion: legacy.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Content: content}
				oldWire, oldErr = legacy.EncodeResponse(t.Context(), response, p.EvaluationContext{})
				response.SchemaVersion = draft.CoreVersion()
				newWire, newErr = draft.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			}
			if oldErr != nil || newErr != nil || !bytes.Equal(oldWire, newWire) {
				t.Fatalf("message runs changed: old=%s new=%s errors=%v / %v", oldWire, newWire, oldErr, newErr)
			}
		})
	}
}

func TestResponsesPackageDerivedTextAndNullFilters(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"id":"r","model":"m","output":[],"output_text":"","content_filters":null}`,
		`{"id":"r","model":"m","output_text":"A&B","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A\u0026"}]},{"type":"function_call","call_id":"c","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"B"}]}]}`,
		`{"id":"r","model":"m","output":[],"content_filters":{"vendor":true}}`,
		`{"id":"r","model":"m","output_text":"short","output":[{"role":"assistant","content":"short"}]}`,
		`{"id":"r","model":"m","output_text":"","output":[{"type":"message","role":"assistant"}]}`,
	} {
		oldResponse, oldErr := legacy.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		newResponse, newErr := draft.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		if oldErr != nil || newErr != nil {
			t.Fatal("valid response was rejected", oldErr, newErr)
		}
		oldResponse.SchemaVersion = draft.CoreVersion()
		want, _ := p.EncodeValue(oldResponse)
		got, _ := p.EncodeValue(newResponse)
		checkPackageMigration(t, want.Bytes(), got.Bytes(), nil, nil, packageMigrationGap{})
		oldResponse.SchemaVersion = legacy.CoreVersion()
		oldResponse.Native, newResponse.Native = nil, nil
		oldWire, oldErr := legacy.EncodeResponse(t.Context(), oldResponse, p.EvaluationContext{})
		newWire, newErr := draft.EncodeResponse(t.Context(), newResponse, p.EvaluationContext{})
		checkPackageMigration(t, oldWire, newWire, oldErr, newErr, packageMigrationGap{})
	}
	for _, raw := range []string{
		`{"output":[],"output_text":null}`,
		`{"output":[],"output_text":42}`,
		`{"output":[],"output_text":[]}`,
		`{"output":[],"output_text":"different"}`,
		`{"output_text":""}`,
		`{"output":null,"output_text":""}`,
		`{"output_text":"","output":[{"type":"message","role":"assistant","content":null}]}`,
	} {
		_, oldErr := legacy.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		_, newErr := draft.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		var oldFailure, newFailure *p.ConversionError
		if !errors.As(oldErr, &oldFailure) || !errors.As(newErr, &newFailure) || len(oldFailure.Issues) != 1 || len(newFailure.Issues) != 1 {
			t.Fatal("derived-text rejection was lost", raw, oldErr, newErr)
		}
		before, after := oldFailure.Issues[0], newFailure.Issues[0]
		if after.Code != p.UpstreamContractViolation || before.Path != after.Path {
			t.Fatal("derived-text rejection lost its external contract boundary", before, after)
		}
		if before.Stage != after.Stage || before.Direction != after.Direction || before.Reason != after.Reason {
			t.Log("diagnostic difference retained for migration review", before, after)
		}
	}
}

func TestResponsesPackageDraftMigration(t *testing.T) {
	legacyRaw, err := os.ReadFile("definitions/openai-responses.json")
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
	draft := compileBuiltinPackage(t, "openai-responses")
	checkResponsesResponseExtensions(t, legacy, draft)
	for _, sample := range definition.Samples {
		if sample.Direction == p.DecodeEvent || sample.Direction == p.EncodeEvent {
			continue
		}
		options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
		t.Run(sample.ID, func(t *testing.T) {
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
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
				want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
				got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			})
		}
	}
	for _, fixture := range []struct{ name, wire string }{
		{"null-reasoning-history", `{"model":"m","input":[{"type":"reasoning","id":"r","content":null,"summary":[],"encrypted_content":"synthetic-sealed"},{"role":"user","content":"hi"}]}`},
		{"instructions-and-choice", `{"model":"m","instructions":"system","input":"hi","reasoning":{"effort":"high","summary":"auto"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"lookup"}}`},
	} {
		for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
			t.Run("boundary/"+fixture.name+"/"+string(direction), func(t *testing.T) {
				options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
				sample := p.Sample{Direction: direction, Input: testValue(t, fixture.wire)}
				if direction == p.EncodeRequest {
					request, err := legacy.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
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
	// Compare the decoded owner directly: root Native can conceal an omitted
	// extension when the unchanged request is encoded back to its source.
	for _, fixture := range []struct{ name, wire string }{
		{"root-and-cache", `{"model":"m","input":"hi","future":{"count":9007199254740993,"empty":null},"prompt_cache_options":{"mode":"explicit","ttl":"30m","future":{"items":[null,1e2,"value"]}}}`},
		{"cache-only", `{"model":"m","input":"hi","prompt_cache_options":{"future":null}}`},
		{"empty-cache", `{"model":"m","input":"hi","prompt_cache_options":{}}`},
		{"null-cache", `{"model":"m","input":"hi","prompt_cache_options":null}`},
	} {
		t.Run("owner-decode/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, fixture.wire)}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			if wantErr != nil {
				t.Fatal(wantErr)
			}
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
		t.Run("owner-encode/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			original, err := legacy.DecodeRequest(t.Context(), []byte(fixture.wire), options)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := draft.DecodeRequest(t.Context(), []byte(fixture.wire), options)
			if err != nil {
				t.Fatal(err)
			}
			for _, edit := range []string{"native", "model", "without-root-native"} {
				t.Run(edit, func(t *testing.T) {
					oldRequest, newRequest := original.Clone(), decoded.Clone()
					switch edit {
					case "model":
						oldRequest.Model, newRequest.Model = p.StringValue("routed"), p.StringValue("routed")
					case "without-root-native":
						oldRequest.Native, newRequest.Native = nil, nil
					}
					want, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
					if wantErr != nil {
						t.Fatal(wantErr)
					}
					got, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
					checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
					if edit == "native" && !bytes.Equal(got, []byte(fixture.wire)) {
						t.Fatal("unchanged request lost original wire bytes", string(got))
					}
				})
			}
		})
	}
	checkResponsesModelOwnerEncoding(t, legacy, draft)
	checkPackageTrackingUsers(t, legacy, draft)
	checkPackageMediaURIs(t, legacy, draft, definition)
}

func checkResponsesResponseExtensions(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	const base = `{ "id":"r","model":"m","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"future":{"n":9007199254740993,"null":null,"values":[1e2,"<"]} }`
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct{ name, extra string }{
		{"unknown", ""},
		{"metadata", `,"service_tier":"default","metadata":{"trace":"synthetic","n":9007199254740993},"store":false,"temperature":0.25,"tools":[],"reasoning":{},"text":{},"user":"synthetic-user"`},
		{"null-metadata", `,"service_tier":null,"metadata":null,"tools":null,"store":null`},
	} {
		raw := strings.TrimSuffix(base, " }") + fixture.extra + " }"
		t.Run("response-extensions/"+fixture.name, func(t *testing.T) {
			original, err := legacy.DecodeResponse(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := draft.DecodeResponse(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			original.SchemaVersion = p.ProtocolCoreVersion
			want, wantErr := p.EncodeValue(original)
			got, gotErr := p.EncodeValue(decoded)
			checkPackageMigration(t, want.Bytes(), got.Bytes(), wantErr, gotErr, packageMigrationGap{})
			original.SchemaVersion = p.SemanticSchemaVersion
			for _, mode := range []string{"native", "changed-model", "rebuilt"} {
				t.Run(mode, func(t *testing.T) {
					before, after := *original, *decoded
					if mode == "changed-model" {
						before.Model, after.Model = p.StringValue("routed"), p.StringValue("routed")
					} else if mode == "rebuilt" {
						before.Native, after.Native = nil, nil
					}
					input, err := p.EncodeValue(after)
					if err != nil {
						t.Fatal(err)
					}
					want, wantErr := legacy.EncodeResponse(t.Context(), &before, options)
					got, gotErr := draft.EncodeResponse(t.Context(), &after, options)
					checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
					if mode == "native" && string(got) != raw {
						t.Fatal("unchanged response lost original bytes")
					}
					current, err := p.EncodeValue(after)
					if err != nil || current != input {
						t.Fatal("response encoding changed caller input", err)
					}
				})
			}
		})
	}
	for _, fixture := range []struct{ field, value string }{{"service_tier", "1"}, {"metadata", "[]"}, {"store", `"false"`}} {
		t.Run("response-metadata-type/"+fixture.field, func(t *testing.T) {
			raw := strings.TrimSuffix(base, " }") + `,"` + fixture.field + `":` + fixture.value + " }"
			_, wantErr := legacy.DecodeResponse(t.Context(), []byte(raw), options)
			_, gotErr := draft.DecodeResponse(t.Context(), []byte(raw), options)
			var before, after *p.ConversionError
			if !errors.As(wantErr, &before) || !errors.As(gotErr, &after) || len(before.Issues) != 1 || len(after.Issues) != 1 {
				t.Fatal("invalid response metadata lost rejection", wantErr, gotErr)
			}
			want, got := before.Issues[0], after.Issues[0]
			if want.Code != got.Code || want.Path != got.Path || want.Reason != got.Reason || want.Stage != "stream" || want.Direction != p.DecodeEvent || got.Stage != "wire" || got.Direction != p.DecodeResponse {
				t.Fatal("metadata type error changed its public attribution", want, got)
			}
			t.Log("NOT CLOSED: old JSON metadata rejection is labeled as a stream event; candidate uses its response wire boundary")
		})
	}
}

func checkResponsesModelOwnerEncoding(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	base, err := legacy.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	base.Native = nil
	// Direct EncodeRequest retains arbitrary model values. The gateway's final
	// wire validation is a separate boundary and must not narrow this comparison.
	for _, fixture := range []struct{ name, model, owner, conflict string }{
		{"missing", "", "", ""},
		{"null", `null`, "", ""},
		{"empty-string", `""`, "", ""},
		{"boolean", `false`, "", ""},
		{"number", `1e2`, "", ""},
		{"array", `[null,9007199254740993]`, "", ""},
		{"object", `{"nested":{"original":1}}`, `{"future":true}`, ""},
		{"owner-empty", `"m"`, `{}`, ""},
		{"owner-fills-model", "", `{"model":{"future":null}}`, ""},
		{"owner-merges-model", `{"nested":{"original":1}}`, `{"model":{"nested":{"extra":2},"future":null}}`, ""},
		{"equal-string-conflict", `"m"`, `{"model":"m"}`, "/model"},
		{"null-conflict", `null`, `{"model":null}`, "/model"},
		{"array-conflict", `[]`, `{"model":[]}`, "/model"},
		{"nested-conflict", `{"nested":{"value":1}}`, `{"model":{"nested":{"value":1}}}`, "/model/nested/value"},
		{"object-scalar-conflict", `{"value":1}`, `{"model":true}`, "/model"},
	} {
		t.Run("model-owner/"+fixture.name, func(t *testing.T) {
			oldRequest := base.Clone()
			oldRequest.Model = p.Value{}
			if fixture.model != "" {
				oldRequest.Model = testValue(t, fixture.model)
			}
			if fixture.owner != "" {
				oldRequest.Parameters = p.Object{"wire:openai_responses": testValue(t, fixture.owner)}
			}
			newRequest := oldRequest.Clone()
			newRequest.SchemaVersion = p.ProtocolCoreVersion
			want, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
			got, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
			if fixture.conflict == "" {
				if wantErr != nil {
					t.Fatal(wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
				return
			}
			var oldFailure, newFailure *p.ConversionError
			var conflict *p.ObjectMergeConflictError
			if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 ||
				oldFailure.Issues[0].Code != p.UnsupportedCapability || oldFailure.Issues[0].Stage != "wire" || oldFailure.Issues[0].Path != fixture.conflict {
				t.Fatal("legacy collision boundary changed", wantErr)
			}
			if !errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 ||
				newFailure.Issues[0].Code != p.InvalidInput || newFailure.Issues[0].Stage != "evaluate" ||
				!errors.As(gotErr, &conflict) || conflict.Path != fixture.conflict {
				t.Fatal("deep merge lost the typed collision cause", gotErr)
			}
			t.Log("NOT CLOSED: legacy wire capability diagnostics and DSL evaluation diagnostics differ", wantErr, gotErr)
		})
	}
	for _, owner := range []string{`null`, `false`, `[]`} {
		t.Run("invalid-owner/"+owner, func(t *testing.T) {
			oldRequest := base.Clone()
			oldRequest.Parameters = p.Object{"wire:openai_responses": testValue(t, owner)}
			newRequest := oldRequest.Clone()
			newRequest.SchemaVersion = p.ProtocolCoreVersion
			_, wantErr := legacy.EncodeRequest(t.Context(), oldRequest, options)
			_, gotErr := draft.EncodeRequest(t.Context(), newRequest, options)
			var oldFailure, newFailure *p.ConversionError
			if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 ||
				oldFailure.Issues[0].Code != p.InvalidInput || oldFailure.Issues[0].Stage != "runtime" ||
				!strings.Contains(wantErr.Error(), "expected JSON object") ||
				!errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 ||
				newFailure.Issues[0].Code != p.InvalidInput || newFailure.Issues[0].Stage != "evaluate" || errors.Unwrap(gotErr) == nil {
				t.Fatal("non-object owner was accepted or lost its cause", wantErr, gotErr)
			}
			t.Log("NOT CLOSED: invalid owner diagnostics differ because the emit condition also reads the owner", wantErr, gotErr)
		})
	}
}
