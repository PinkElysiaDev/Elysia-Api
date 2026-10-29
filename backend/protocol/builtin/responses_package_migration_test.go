package builtin

import (
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The four JSON directions are a standalone declaration candidate. The package
// compiler has no codec modules, and its samples are not activation evidence.
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
	draftRaw, err := os.ReadFile("packages/openai-responses.draft.json")
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
	}
	checkPackageMediaURIs(t, legacy, draft, definition)
}
