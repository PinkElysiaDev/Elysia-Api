package builtin

import (
	"bytes"
	"errors"
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageHistoryOwner(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	raw, err := os.ReadFile("packages/anthropic.draft.json")
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, issues := compiler.CompilePackage(raw)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	const canonical = `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`
	for _, fixture := range []struct{ name, control, extra string }{
		{"string", canonical, ""},
		{"object", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all"}}]}`, ""},
		{"escaped", `{ "edits" : [ { "keep" : "a\u006cl", "type" : "clear_thinking_20251015" } ] }`, ""},
		{"generation-controls", canonical, `,"thinking":{"type":"enabled","budget_tokens":1024,"future":true},"output_config":{"effort":"high"}`},
		{"empty-generation-controls", canonical, `,"thinking":{},"output_config":null`},
		{"other-owners", canonical, `,"future_root":{"n":9007199254740993},"prompt_cache_options":{"future":true}`},
	} {
		t.Run("known/"+fixture.name, func(t *testing.T) {
			wire := []byte(`{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":"hi"}],"context_management":` + fixture.control + fixture.extra + `}`)
			original, err := legacy.DecodeRequest(t.Context(), wire, options)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := draft.DecodeRequest(t.Context(), wire, options)
			if err != nil {
				t.Fatal(err)
			}
			// Core2 deliberately moves this recognized owner out of wire params.
			// Everything outside this explicit move stays in the byte comparison.
			expected := original.Clone()
			expected.SchemaVersion = p.ProtocolCoreVersion
			if expected.Reasoning == nil {
				expected.Reasoning = &p.ReasoningIntent{}
			}
			expected.Reasoning.HistoryRetention = p.StringValue("all")
			removeAnthropicHistoryParameter(t, expected)
			want, err := p.EncodeValue(expected)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.EncodeValue(decoded)
			if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Fatalf("history owner move changed other core data: %v\nwant: %s\ngot: %s", err, want.JSONString(), got.JSONString())
			}
			preserved, err := draft.EncodeRequest(t.Context(), decoded, options)
			if err != nil || !bytes.Equal(preserved, wire) {
				t.Fatal("native history representation changed", err, string(preserved))
			}
			for _, edit := range []string{"model", "remove-history"} {
				oldEdit, newEdit := original.Clone(), decoded.Clone()
				if edit == "model" {
					oldEdit.Model, newEdit.Model = p.StringValue("routed"), p.StringValue("routed")
				} else {
					removeAnthropicHistoryParameter(t, oldEdit)
					newEdit.Reasoning.HistoryRetention = p.Value{}
					if *newEdit.Reasoning == (p.ReasoningIntent{}) {
						newEdit.Reasoning = nil
					}
				}
				want, wantErr := legacy.EncodeRequest(t.Context(), oldEdit, options)
				got, gotErr := draft.EncodeRequest(t.Context(), newEdit, options)
				if wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
					t.Fatalf("%s changed native rendering: old=%v draft=%v\nwant: %s\ngot: %s", edit, wantErr, gotErr, want, got)
				}
			}
			original.Native, decoded.Native = nil, nil
			oldWire, err := legacy.EncodeRequest(t.Context(), original, options)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := testValue(t, string(oldWire)).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			// Without Native, the declared canonical variant replaces wire aliases.
			fields["context_management"] = testValue(t, canonical)
			want, err = p.EncodeValue(fields)
			if err != nil {
				t.Fatal(err)
			}
			canonicalWire, err := draft.EncodeRequest(t.Context(), decoded, options)
			if err != nil || !bytes.Equal(want.Bytes(), canonicalWire) {
				t.Fatal("native-free rendering changed fields outside the canonical history variant", err, want.JSONString(), string(canonicalWire))
			}
		})
	}
	for _, fixture := range []struct{ name, control string }{
		{"null", `null`},
		{"empty-edits", `{"edits":[]}`},
		{"missing-keep", `{"edits":[{"type":"clear_thinking_20251015"}]}`},
		{"finite-keep", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}}]}`},
		{"keep-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`},
		{"multiple-edits", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919"}]}`},
		{"owner-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}],"future":true}`},
	} {
		t.Run("unmatched/"+fixture.name, func(t *testing.T) {
			wire := testValue(t, `{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":"hi"}],"context_management":`+fixture.control+`}`)
			sample := p.Sample{Direction: p.DecodeRequest, Input: wire}
			want, wantErr := executePackageMigrationSample(t, legacy, sample, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, sample, options, true)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
			request, err := draft.DecodeRequest(t.Context(), wire.Bytes(), options)
			if err != nil {
				t.Fatal(err)
			}
			preserved, err := draft.EncodeRequest(t.Context(), request, options)
			if err != nil || !bytes.Equal(preserved, wire.Bytes()) {
				t.Fatal("unmatched native control changed", err, string(preserved))
			}
			original, err := legacy.DecodeRequest(t.Context(), wire.Bytes(), options)
			if err != nil {
				t.Fatal(err)
			}
			original.Native, request.Native = nil, nil
			want, wantErr = legacy.EncodeRequest(t.Context(), original, options)
			got, gotErr = draft.EncodeRequest(t.Context(), request, options)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
	t.Run("canonical-unmatched-conflict", func(t *testing.T) {
		request, err := draft.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[],"context_management":`+canonical+`}`), options)
		if err != nil {
			t.Fatal(err)
		}
		request.Parameters["wire:claude"] = testValue(t, `{"context_management":null}`)
		_, err = draft.EncodeRequest(t.Context(), request, options)
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.ConversionRejected || failure.Issues[0].Path != "/reasoning/historyRetention" {
			t.Fatal("canonical history and unmatched wire control were combined", err)
		}
	})
}

func removeAnthropicHistoryParameter(t *testing.T, request *p.Request) {
	t.Helper()
	fields, err := request.Parameters["wire:claude"].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	delete(fields, "context_management")
	if len(fields) == 0 {
		delete(request.Parameters, "wire:claude")
		return
	}
	request.Parameters["wire:claude"], err = p.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
}
