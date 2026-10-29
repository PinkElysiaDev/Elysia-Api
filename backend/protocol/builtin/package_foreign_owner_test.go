package builtin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageDraftForeignWireOwners(t *testing.T) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ name, file, owner, foreign string }{
		{"anthropic", "anthropic.draft.json", "wire:claude", "wire:gemini"},
		{"gemini", "google-generate-content.draft.json", "wire:gemini", "wire:claude"},
		{"chat", "openai-chat.draft.json", "wire:openai_chat", "wire:claude"},
		{"responses", "openai-responses.draft.json", "wire:openai_responses", "wire:claude"},
	} {
		raw, err := os.ReadFile(filepath.Join("packages", target.file))
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.CompilePackage(raw)
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		request := func() *p.Request {
			return &p.Request{
				SchemaVersion: p.ProtocolCoreVersion,
				Model:         p.StringValue("m"),
				Parameters:    p.Object{"max_output_tokens": testValue(t, "64")},
				Content:       []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}},
			}
		}
		for _, owner := range []string{"wire:custom", target.foreign} {
			for _, fixture := range []struct{ name, value string }{
				{"empty-object", `{}`}, {"empty-array", `[]`}, {"null", `null`},
				{"empty-string", `""`}, {"zero", `0`}, {"false", `false`},
			} {
				t.Run(target.name+"/"+owner+"/"+fixture.name, func(t *testing.T) {
					input := request()
					input.Parameters[owner] = testValue(t, fixture.value)
					_, err := compiled.EncodeRequest(t.Context(), input, p.EvaluationContext{})
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) != 1 {
						t.Fatal("foreign owner was accepted or failed outside its declared guard", err)
					}
					issue := failure.Issues[0]
					if issue.Code != p.ConversionRejected || issue.Direction != p.EncodeRequest || issue.Stage != "mapping.effects" || issue.Path != "/parameters" || issue.RuleID != "foreign-wire-owner" {
						t.Fatal("foreign owner rejection has the wrong declaration", issue)
					}
				})
			}
		}
		for _, shape := range []string{"absent-owner", "own-empty-object", "ordinary-parameter", "nil-parameters"} {
			t.Run(target.name+"/"+shape, func(t *testing.T) {
				input := request()
				switch shape {
				case "own-empty-object":
					input.Parameters[target.owner] = testValue(t, `{}`)
				case "ordinary-parameter":
					input.Parameters["temperature"] = testValue(t, `0.5`)
				case "nil-parameters":
					input.Parameters = nil
				}
				_, err := compiled.EncodeRequest(t.Context(), input, p.EvaluationContext{})
				if shape != "nil-parameters" && err != nil {
					t.Fatal("foreign-owner guard changed an existing supported fixture", err)
				}
				// Missing mandatory target fields can still fail after this guard.
				var failure *p.ConversionError
				if errors.As(err, &failure) {
					for _, issue := range failure.Issues {
						if issue.RuleID == "foreign-wire-owner" {
							t.Fatal("guard treated a non-foreign owner as foreign", issue)
						}
					}
				}
			})
		}
	}
}
