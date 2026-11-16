package builtin

import (
	"bytes"
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageHistoryOwner(t *testing.T) {
	const canonical = `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`
	const canonicalMessages = `[{"role":"user","content":[{"type":"text","text":"hi"}]}]`
	compiled := testCompiled(t, Anthropic)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, sample := range []struct{ name, control, extra string }{
		{"string", canonical, ""},
		{"object", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all"}}]}`, ""},
		{"escaped", `{ "edits" : [ { "keep" : "a\u006cl", "type" : "clear_thinking_20251015" } ] }`, ""},
		{"generation-controls", canonical, `,"thinking":{"type":"enabled","budget_tokens":1024,"future":true},"output_config":{"effort":"high"}`},
		{"empty-generation-controls", canonical, `,"thinking":{},"output_config":null`},
		{"other-owners", canonical, `,"future_root":{"n":9007199254740993},"prompt_cache_options":{"future":true}`},
	} {
		t.Run("known/"+sample.name, func(t *testing.T) {
			raw := `{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":"hi"}],"context_management":` + sample.control + sample.extra + `}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			if request.Reasoning == nil || !request.Reasoning.HistoryRetention.IsString("all") {
				t.Fatal("recognized history retention did not enter core2", request.Reasoning)
			}
			preserved, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil || !bytes.Equal(preserved, []byte(raw)) {
				t.Fatal("native history representation changed", string(preserved), err)
			}
			for _, edit := range []string{"model", "remove-history", "without-native"} {
				candidate := request.Clone()
				fields, err := testValue(t, raw).ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				switch edit {
				case "model":
					candidate.Model = p.StringValue("routed")
					fields["model"] = p.StringValue("routed")
				case "remove-history":
					candidate.Reasoning.HistoryRetention = p.Value{}
					if *candidate.Reasoning == (p.ReasoningIntent{}) {
						candidate.Reasoning = nil
					}
					delete(fields, "context_management")
				case "without-native":
					candidate.Native = nil
					fields["context_management"] = testValue(t, canonical)
					fields["messages"] = testValue(t, canonicalMessages)
				}
				want, err := p.EncodeValue(fields)
				if err != nil {
					t.Fatal(err)
				}
				got, err := compiled.EncodeRequestValue(t.Context(), candidate, options)
				if err != nil || !equivalentJSON(want, got) {
					t.Fatal("history owner edit changed an unrelated field", edit, want, got, err)
				}
			}
		})
	}
	for _, control := range []string{
		`null`, `{"edits":[]}`, `{"edits":[{"type":"clear_thinking_20251015"}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919"}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}],"future":true}`,
	} {
		t.Run("unmatched/"+control, func(t *testing.T) {
			raw := `{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":"hi"}],"context_management":` + control + `}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			if request.Reasoning != nil && !request.Reasoning.HistoryRetention.IsZero() {
				t.Fatal("unknown history retention was misclassified", request.Reasoning)
			}
			for _, hasNative := range []bool{true, false} {
				want := raw
				if !hasNative {
					request.Native = nil
					want = `{"model":"m","max_tokens":8192,"messages":` + canonicalMessages + `,"context_management":` + control + `}`
				}
				wire, err := compiled.EncodeRequest(t.Context(), request, options)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, wire, want)
			}
		})
	}
	t.Run("canonical-unmatched-conflict", func(t *testing.T) {
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[],"context_management":`+canonical+`}`), options)
		if err != nil {
			t.Fatal(err)
		}
		setRequestParameter(request, "wire:claude", testValue(t, `{"context_management":null}`))
		_, err = compiled.EncodeRequest(t.Context(), request, options)
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.ConversionRejected || failure.Issues[0].Path != "/reasoning/historyRetention" {
			t.Fatal("canonical history and unmatched wire control were combined", err)
		}
	})
}
