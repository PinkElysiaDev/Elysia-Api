package builtin

import (
	"os"
	"path/filepath"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// These JSON packages cover the history-control slice. They are not the shipped
// codecs and do not establish complete request/response protocol coverage.
func TestHistoryDeclarationsMatchExistingRequestMatrix(t *testing.T) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prototypes := map[string]*p.Compiled{}
	for codec, name := range map[string]string{Anthropic: "anthropic", Gemini: "gemini", Chat: "chat", Responses: "responses"} {
		raw, err := os.ReadFile(filepath.Join("..", "testdata", "history", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.CompilePackage(raw)
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		prototypes[codec] = compiled
	}
	for _, tc := range []struct{ name, control, extra string }{
		{"string", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, ""},
		{"object", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all"}}]}`, ""},
		{"escaped", `{"edits":[{"type":"clear_thinking_20251015","keep":"a\u006cl"}]}`, ""},
		{"missing-keep", `{"edits":[{"type":"clear_thinking_20251015"}]}`, ""},
		{"finite", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}}]}`, ""},
		{"keep-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`, ""},
		{"edit-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":"all","future":true}]}`, ""},
		{"object-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}],"future":true}`, ""},
		{"mixed", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919"}]}`, ""},
		{"root-extra", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, `,"future":{"n":9007199254740993}`},
		{"null", `null`, ""},
	} {
		for _, codec := range []string{Anthropic, Gemini, Chat, Responses} {
			for _, mode := range []string{"compatible", "strict"} {
				t.Run(tc.name+"/"+codec+"/"+mode, func(t *testing.T) {
					raw := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"context_management":` + tc.control + tc.extra + `}`
					convert := func(source, target *p.Compiled) ([]byte, error) {
						request, err := source.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
						if err != nil {
							return nil, err
						}
						policy := p.DefaultConversionPolicy(source, target)
						policy.Mode = mode
						conversion, err := p.ResolveConversion(policy)
						if err != nil {
							return nil, err
						}
						projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
						if err != nil {
							return nil, err
						}
						return target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
					}
					before, beforeErr := convert(shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, codec))
					after, afterErr := convert(prototypes[Anthropic], prototypes[codec])
					if (beforeErr == nil) != (afterErr == nil) {
						t.Fatalf("acceptance changed: before %v, after %v", beforeErr, afterErr)
					}
					if beforeErr == nil {
						sameJSON(t, after, string(before))
					}
				})
			}
		}
	}
}
