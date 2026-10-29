package builtin

import (
	"fmt"
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The candidate remains outside the production loader. Compare the actual
// programs directly; no migration guard is removed and no codec is installed
// in the package compiler. Known gaps are explicit, not activation evidence.
func TestAnthropicPackageDraftMigration(t *testing.T) {
	legacyRaw, err := os.ReadFile("definitions/anthropic-messages.json")
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
	draftRaw, err := os.ReadFile("packages/anthropic.draft.json")
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
		t.Run(sample.ID, func(t *testing.T) {
			options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
	for _, fixture := range []struct {
		name, wire string
		encode     bool
		gap        packageMigrationGap
	}{
		{"cache-null", `{"model":"m","max_tokens":10,"cache_control":null,"messages":[{"role":"user","content":"hello"}]}`, false, packageMigrationGap{}},
		{"cache-null-ttl", `{"model":"m","max_tokens":10,"cache_control":{"type":"ephemeral","ttl":null,"future":1},"messages":[{"role":"user","content":"hello"}]}`, false, packageMigrationGap{}},
		{"unknown-fields", `{"model":"m","max_tokens":10,"future":{"n":9007199254740993},"messages":[{"role":"user","content":[{"type":"text","text":"hello","future":{"items":[1,2]}}]}]}`, false, packageMigrationGap{}},
		{"opaque-without-type", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"future":{"n":1}}]}]}`, false, packageMigrationGap{}},
		{"direct-caller", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":{"type":"direct"}}]}]}`, false, packageMigrationGap{}},
		{"caller-extension", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":{"type":"direct","extra":1}}]}]}`, false, packageMigrationGap{}},
		{"tool-result-content-array", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"content":[{"type":"opaque-user-data"}]}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"text","text":"done","future":1},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}]}`, false, packageMigrationGap{}},
		{"raw-reference-key-before-escaping", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"<","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"\u003c","content":"done"}]}]}`, false, packageMigrationGap{}},
		{"invalid-caller", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":"invalid"}]}]}`, false, packageMigrationGap{reason: "structured rejection diagnostic parity", rejection: true}},
		{"invalid-tool-error", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"done","is_error":"invalid"}]}]}`, false, packageMigrationGap{reason: "structured rejection diagnostic parity", rejection: true}},
		{"thinking-controls", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`, false, packageMigrationGap{}},
		{"inline-system-without-root-native", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"inline instruction"}]}`, true, packageMigrationGap{}},
	} {
		t.Run("boundary/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, fixture.wire)}
			if fixture.encode {
				request, err := legacy.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
				if err != nil {
					t.Fatal(err)
				}
				request.Native = nil
				sample.Input, err = p.EncodeValue(request)
				if err != nil {
					t.Fatal(err)
				}
				sample.Direction = p.EncodeRequest
			}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, fixture.gap)
		})
	}
	checkAnthropicReasoningMigration(t, legacy, draft)
	checkAnthropicNativeEdits(t, legacy, draft)
}

func checkAnthropicNativeEdits(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	const hostedWire = `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"web_search_20250305","name":"original","max_uses":2,"vendor":{"n":9007199254740993},"cache_control":{"type":"ephemeral","ttl":"1h","future":true}}]}`
	for _, fixture := range []struct {
		name, wire string
		edit       func(*p.Request)
	}{
		{"hosted-current-name-remove-cache", hostedWire, func(request *p.Request) {
			request.Tools[0].Name = p.StringValue("renamed")
			request.Tools[0].Cache = nil
		}},
		{"opaque-remove-name-null-cache", hostedWire, func(request *p.Request) {
			request.Tools[0].Kind = p.OpaqueTool
			request.Tools[0].Name = p.Value{}
			request.Tools[0].Cache = []p.CacheIntent{{Kind: "breakpoint", Location: "tool", Value: nullValue()}}
		}},
		{"inline-system-current-position-and-content", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"inline instruction","output_config":{"effort":"high"}}]}`, func(request *p.Request) {
			request.Content[1].Children[0].Payload = p.StringValue("edited instruction")
			request.Content[0], request.Content[1] = request.Content[1], request.Content[0]
		}},
	} {
		t.Run("native-edit/"+fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
			request, err := legacy.DecodeRequest(t.Context(), []byte(fixture.wire), options)
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			fixture.edit(request)
			input, err := p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			sample := p.Sample{Direction: p.EncodeRequest, Input: input}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("native edit rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
}

func checkAnthropicReasoningMigration(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	for _, fixture := range []struct {
		name, fields string
		interleaved  bool
		maxTokens    int
	}{
		{"enabled-with-unknowns", `"thinking":{"type":"enabled","budget_tokens":4096,"future_thinking":true},"output_config":{"effort":"high","future_config":true}`, false, 8192},
		{"disabled-generation-effort", `"thinking":{"type":"disabled"},"output_config":{"effort":"high","future_config":true}`, false, 8192},
		{"adaptive-display", `"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"high"}`, false, 8192},
		{"between-tools", `"thinking":{"type":"between_tools"},"output_config":{"effort":"high"}`, false, 8192},
		{"null-display", `"thinking":{"type":"enabled","budget_tokens":1024,"display":null}`, false, 8192},
		{"empty-controls", `"thinking":{},"output_config":{}`, false, 8192},
		{"null-controls", `"thinking":null,"output_config":null`, false, 8192},
		{"unknown-only-output", `"output_config":{"future_config":true}`, false, 8192},
		{"effort-null", `"output_config":{"effort":null}`, false, 8192},
		{"empty-thinking-with-effort", `"thinking":{},"output_config":{"effort":"high"}`, false, 8192},
		{"null-thinking-with-effort", `"thinking":null,"output_config":{"effort":"high"}`, false, 8192},
		{"interleaved", `"thinking":{"type":"enabled","budget_tokens":4096},"tools":[{"name":"lookup","input_schema":{"type":"object"}}]`, true, 2048},
		{"interleaved-without-tools", `"thinking":{"type":"enabled","budget_tokens":4096}`, true, 2048},
		{"ordinary-context-cannot-enable", `"thinking":{"type":"enabled","budget_tokens":4096},"tools":[{"name":"lookup","input_schema":{"type":"object"}}]`, false, 2048},
	} {
		t.Run("reasoning/"+fixture.name, func(t *testing.T) {
			spoof, _ := p.EncodeValue(!fixture.interleaved)
			options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}, InterleavedThinking: fixture.interleaved, Values: p.Object{"interleavedThinking": spoof}}
			wire := fmt.Sprintf(`{"model":"m","max_tokens":%d,%s,"messages":[{"role":"user","content":"hi"}]}`, fixture.maxTokens, fixture.fields)
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, wire)}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("valid reasoning controls rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			request, err := legacy.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			sample.Input, err = p.EncodeValue(request)
			if err != nil {
				t.Fatal(err)
			}
			sample.Direction = p.EncodeRequest
			want, wantErr = executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr = executePackageMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("valid reasoning rendering rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
	for _, fixture := range []struct{ name, fields string }{
		{"missing-mode", `"thinking":{"future":true}`},
		{"missing-budget", `"thinking":{"type":"enabled"}`},
		{"null-budget", `"thinking":{"type":"enabled","budget_tokens":null}`},
		{"small-budget", `"thinking":{"type":"enabled","budget_tokens":1023}`},
		{"adaptive-null-budget", `"thinking":{"type":"adaptive","budget_tokens":null}`},
		{"unknown-display", `"thinking":{"type":"adaptive","display":"future"}`},
		{"array-display", `"thinking":{"type":"adaptive","display":[]}`},
		{"effort-none", `"output_config":{"effort":"none"}`},
		{"scalar-output-config", `"output_config":false`},
	} {
		t.Run("reasoning/reject-"+fixture.name, func(t *testing.T) {
			wire := `{"model":"m","max_tokens":8192,` + fixture.fields + `,"messages":[{"role":"user","content":"hi"}]}`
			sample := p.Sample{Direction: p.DecodeRequest, Input: testValue(t, wire)}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, p.EvaluationContext{}, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, p.EvaluationContext{}, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{reason: "reasoning wire rejection diagnostic parity", rejection: true})
		})
	}
}
