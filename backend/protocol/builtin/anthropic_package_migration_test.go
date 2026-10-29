package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
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
			want, wantErr := executeAnthropicMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executeAnthropicMigrationSample(t, draft, sample, options, true)
			checkAnthropicMigration(t, want, got, wantErr, gotErr, anthropicMigrationGap{})
		})
	}
	for _, fixture := range []struct {
		name, wire string
		encode     bool
		gap        anthropicMigrationGap
	}{
		{"cache-null", `{"model":"m","max_tokens":10,"cache_control":null,"messages":[{"role":"user","content":"hello"}]}`, false, anthropicMigrationGap{}},
		{"cache-null-ttl", `{"model":"m","max_tokens":10,"cache_control":{"type":"ephemeral","ttl":null,"future":1},"messages":[{"role":"user","content":"hello"}]}`, false, anthropicMigrationGap{}},
		{"unknown-fields", `{"model":"m","max_tokens":10,"future":{"n":9007199254740993},"messages":[{"role":"user","content":[{"type":"text","text":"hello","future":{"items":[1,2]}}]}]}`, false, anthropicMigrationGap{}},
		{"opaque-without-type", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"future":{"n":1}}]}]}`, false, anthropicMigrationGap{}},
		{"direct-caller", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":{"type":"direct"}}]}]}`, false, anthropicMigrationGap{}},
		{"caller-extension", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":{"type":"direct","extra":1}}]}]}`, false, anthropicMigrationGap{}},
		{"tool-result-content-array", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"content":[{"type":"opaque-user-data"}]}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"text","text":"done","future":1},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}]}`, false, anthropicMigrationGap{}},
		{"raw-reference-key-before-escaping", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"<","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"\u003c","content":"done"}]}]}`, false, anthropicMigrationGap{}},
		{"invalid-caller", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{},"caller":"invalid"}]}]}`, false, anthropicMigrationGap{reason: "structured rejection diagnostic parity", rejection: true}},
		{"invalid-tool-error", `{"model":"m","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"done","is_error":"invalid"}]}]}`, false, anthropicMigrationGap{reason: "structured rejection diagnostic parity", rejection: true}},
		{"thinking-controls", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`, false, anthropicMigrationGap{}},
		{"inline-system-without-root-native", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"inline instruction"}]}`, true, anthropicMigrationGap{}},
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
			want, wantErr := executeAnthropicMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executeAnthropicMigrationSample(t, draft, sample, options, true)
			checkAnthropicMigration(t, want, got, wantErr, gotErr, fixture.gap)
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
			want, wantErr := executeAnthropicMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executeAnthropicMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("native edit rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkAnthropicMigration(t, want, got, wantErr, gotErr, anthropicMigrationGap{})
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
			want, wantErr := executeAnthropicMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executeAnthropicMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("valid reasoning controls rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkAnthropicMigration(t, want, got, wantErr, gotErr, anthropicMigrationGap{})
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
			want, wantErr = executeAnthropicMigrationSample(t, legacy, sample, options, false)
			got, gotErr = executeAnthropicMigrationSample(t, draft, sample, options, true)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("valid reasoning rendering rejected: old=%v, draft=%v", wantErr, gotErr)
			}
			checkAnthropicMigration(t, want, got, wantErr, gotErr, anthropicMigrationGap{})
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
			want, wantErr := executeAnthropicMigrationSample(t, legacy, sample, p.EvaluationContext{}, false)
			got, gotErr := executeAnthropicMigrationSample(t, draft, sample, p.EvaluationContext{}, true)
			checkAnthropicMigration(t, want, got, wantErr, gotErr, anthropicMigrationGap{reason: "reasoning wire rejection diagnostic parity", rejection: true})
		})
	}
}

type anthropicMigrationGap struct {
	reason, errorText string
	paths             []string
	rejection         bool
}

func checkAnthropicMigration(t *testing.T, want, got []byte, wantErr, gotErr error, gap anthropicMigrationGap) {
	t.Helper()
	isEqual := bytes.Equal(got, want) && migrationError(gotErr) == migrationError(wantErr)
	if gap.reason == "" {
		if !isEqual {
			t.Fatalf("unexpected migration difference %v: old=%v, draft=%v\nold: %s\ndraft: %s", migrationDifference(want, got), wantErr, gotErr, want, got)
		}
		return
	}
	if isEqual {
		t.Fatalf("recorded migration blocker is now closed; update its explicit ledger: %s", gap.reason)
	}
	if gap.paths != nil && (wantErr != nil || gotErr != nil || !reflect.DeepEqual(gap.paths, migrationDifference(want, got))) {
		t.Fatalf("unregistered differences beyond %s: %v; old=%v; draft=%v", gap.reason, migrationDifference(want, got), wantErr, gotErr)
	}
	if gap.errorText != "" && (wantErr != nil || gotErr == nil || !strings.Contains(gotErr.Error(), gap.errorText)) {
		t.Fatalf("unexpected failure beyond %s: old=%v; draft=%v", gap.reason, wantErr, gotErr)
	}
	if gap.rejection && (wantErr == nil || gotErr == nil) {
		t.Fatalf("rejection acceptance changed beyond diagnostic gap: old=%v; draft=%v", wantErr, gotErr)
	}
	if gap.rejection {
		t.Logf("old diagnostics=%s; draft diagnostics=%s", migrationError(wantErr), migrationError(gotErr))
	}
	t.Logf("NOT CLOSED: %s; old error=%v; draft error=%v; differences=%v", gap.reason, wantErr, gotErr, migrationDifference(want, got))
}

func executeAnthropicMigrationSample(t *testing.T, compiled *p.Compiled, sample p.Sample, options p.EvaluationContext, isPackage bool) ([]byte, error) {
	t.Helper()
	switch sample.Direction {
	case p.DecodeRequest:
		request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
		if err != nil {
			return nil, err
		}
		// Version 2 is the new core contract. All business fields and Native
		// evidence remain in the comparison, including number spelling.
		request.SchemaVersion = p.ProtocolCoreVersion
		value, err := p.EncodeValue(request)
		return value.Bytes(), err
	case p.EncodeRequest:
		var request p.Request
		if err := sample.Input.Decode(&request); err != nil {
			return nil, err
		}
		if isPackage {
			request.SchemaVersion = p.ProtocolCoreVersion
		}
		return compiled.EncodeRequest(t.Context(), &request, options)
	case p.DecodeResponse:
		response, err := compiled.DecodeResponse(t.Context(), sample.Input.Bytes(), options)
		if err != nil {
			return nil, err
		}
		response.SchemaVersion = p.ProtocolCoreVersion
		value, err := p.EncodeValue(response)
		return value.Bytes(), err
	case p.EncodeResponse:
		var response p.Response
		if err := sample.Input.Decode(&response); err != nil {
			return nil, err
		}
		if isPackage {
			response.SchemaVersion = p.ProtocolCoreVersion
		}
		return compiled.EncodeResponse(t.Context(), &response, options)
	default:
		t.Fatal("unexpected migration direction", sample.Direction)
		return nil, nil
	}
}

func migrationError(err error) string {
	if err == nil {
		return ""
	}
	var conversion *p.ConversionError
	if errors.As(err, &conversion) {
		value, _ := p.EncodeValue(conversion.Issues)
		return fmt.Sprintf("%T: %s", err, value.JSONString())
	}
	return fmt.Sprintf("%T: %v", err, err)
}

func migrationDifference(want, got []byte) []string {
	if bytes.Equal(want, got) {
		return nil
	}
	left, leftErr := p.ParseValue(want)
	right, rightErr := p.ParseValue(got)
	if leftErr != nil || rightErr != nil {
		return []string{"/"}
	}
	var paths []string
	var visit func(p.Value, p.Value, string)
	visit = func(left, right p.Value, path string) {
		if left == right {
			return
		}
		before := len(paths)
		if left.IsObject() && right.IsObject() {
			first, _ := left.ReadObject()
			second, _ := right.ReadObject()
			keys := map[string]bool{}
			for key := range first {
				keys[key] = true
			}
			for key := range second {
				keys[key] = true
			}
			for key := range keys {
				visit(first[key], second[key], path+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
			}
		} else if left.IsArray() && right.IsArray() {
			var first, second []p.Value
			_ = left.Decode(&first)
			_ = right.Decode(&second)
			if len(first) == len(second) {
				for index := range first {
					visit(first[index], second[index], fmt.Sprintf("%s/%d", path, index))
				}
			}
		}
		if len(paths) == before {
			paths = append(paths, path)
		}
	}
	visit(left, right, "")
	sort.Strings(paths)
	return paths
}
