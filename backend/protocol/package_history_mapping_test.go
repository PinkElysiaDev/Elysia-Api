package protocol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func historyPrototype(t *testing.T, name string) ProtocolPackage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "history", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := DecodeProtocolPackage(raw, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestHistoryPrototypePreservesWholeValues(t *testing.T) {
	source := compilePackageForTest(t, historyPrototype(t, "anthropic"))
	for _, control := range []string{
		`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`,
		`{ "edits" : [ { "type" : "clear_thinking_20251015", "keep" : { "type" : "all" } } ] }`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":"a\u006cl"}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}],"future":true}`,
		`null`,
	} {
		raw := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"context_management":` + control + `}`
		request, err := source.DecodeRequest(t.Context(), []byte(raw), EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		isKnown := !strings.Contains(control, "future") && control != "null"
		if (request.Reasoning != nil && request.Reasoning.HistoryRetention.IsString("all")) != isKnown {
			t.Fatal("whole-value recognition changed", control, request.Reasoning)
		}
		unmatched, err := readValuePointer(request.Parameters["wire:claude"], []string{"context_management"})
		if err != nil || unmatched.IsZero() != isKnown {
			t.Fatal("unmatched control was lost", control, unmatched, err)
		}
		wire, err := source.EncodeRequest(t.Context(), request, EvaluationContext{})
		if err != nil || string(wire) != raw {
			t.Fatal("native representation changed", err, string(wire))
		}
	}
	for _, name := range []string{"anthropic", "gemini", "chat", "responses"} {
		compiled := compilePackageForTest(t, historyPrototype(t, name))
		if report := Verify(t.Context(), compiled); !report.Passed {
			t.Fatal(name, report.Issues)
		}
	}
}

func TestHistoryPrototypePolicyBoundary(t *testing.T) {
	source := compilePackageForTest(t, historyPrototype(t, "anthropic"))
	const raw = `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`
	request, err := source.DecodeRequest(t.Context(), []byte(raw), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gemini", "chat", "responses"} {
		target := compilePackageForTest(t, historyPrototype(t, name))
		for _, mode := range []string{"compatible", "strict"} {
			policy := DefaultConversionPolicy(source, target)
			policy.Mode = mode
			conversion, err := ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
			if name != "gemini" {
				assertPackageRuntimeIssue(t, err, ConversionRejected, historyRetentionPath)
				continue
			}
			if err != nil || projected.Reasoning != nil || !request.Reasoning.HistoryRetention.IsString("all") {
				t.Fatal("default changed unrelated or source state", projected, err)
			}
			if _, err := target.EncodeRequest(t.Context(), projected, EvaluationContext{}); err != nil {
				t.Fatal(err)
			}
			issues := sink.Issues()
			if len(issues) != 1 || issues[0].Code != ConversionNormalized || issues[0].Fidelity != "preserved" || issues[0].RuleID != "request-projection" || issues[0].PolicyHash != conversion.Hash {
				t.Fatal(issues)
			}
			policy.Rules[0].Enabled = false
			disabled, err := ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := disabled.Request(t.Context(), request, ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
			if err != nil || !retained.Reasoning.HistoryRetention.IsString("all") {
				t.Fatal("disabled default still ran", err)
			}
			if _, err := target.EncodeRequest(t.Context(), retained, EvaluationContext{}); err == nil {
				t.Fatal("encoder repeated disabled default")
			}
			override := ConversionPolicy{SchemaVersion: 1, ID: "operator", Rules: []ConversionRule{{ID: "request-projection", Order: 130, Enabled: true, Phase: ConversionRequest, Action: "remove", Path: historyRetentionPath}}}
			replaced, err := ResolveConversion(policy, override)
			if err != nil {
				t.Fatal(err)
			}
			explicit, err := replaced.Request(t.Context(), request, ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
			if mode == "strict" {
				assertPackageRuntimeIssue(t, err, ConversionRejected, historyRetentionPath)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.EncodeRequest(t.Context(), explicit, EvaluationContext{}); err != nil {
				t.Fatal("explicit override did not replace default", err)
			}
		}
	}
	request.Reasoning.Summary = StringValue("concise")
	target := compilePackageForTest(t, historyPrototype(t, "gemini"))
	conversion, _ := ResolveConversion(DefaultConversionPolicy(source, target))
	projected, err := conversion.Request(t.Context(), request, ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
	if err != nil || projected.Reasoning == nil || !projected.Reasoning.Summary.IsString("concise") || !projected.Reasoning.HistoryRetention.IsZero() {
		t.Fatal("default swallowed generation controls", err, projected)
	}
	if _, err := target.EncodeRequest(t.Context(), projected, EvaluationContext{}); err == nil {
		t.Fatal("prototype silently dropped unsupported generation controls")
	}
}

func TestHistoryDefaultCannotRestoreNativeControl(t *testing.T) {
	document := historyPrototype(t, "anthropic")
	target := historyPrototype(t, "gemini")
	encode := document.Mapping.Directions[EncodeRequest]
	for _, effect := range target.Mapping.Directions[EncodeRequest].Effects {
		if effect.Scope == requestFieldScope {
			encode.Effects = append(encode.Effects, effect)
		}
	}
	document.Mapping.Directions[EncodeRequest] = encode
	compiled := compilePackageForTest(t, document)
	for _, hasControl := range []bool{false, true} {
		raw := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}]`
		if hasControl {
			raw += `,"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`
		}
		raw += `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(raw), EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		request.Reasoning = nil // Clearing current state does not authorize replay.
		wire, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
		if hasControl {
			assertPackageRuntimeIssue(t, err, UnsupportedNative, "/native")
		} else if err != nil || string(wire) != raw {
			t.Fatal("trusted baseline without a control was rejected", err, string(wire))
		}
	}
}

func TestHistoryValueMappingRejectsUnprovedRelations(t *testing.T) {
	for _, change := range []func(*ProtocolPackage){
		func(p *ProtocolPackage) {
			mapping := p.Mapping.Directions[DecodeRequest]
			mapping.Program.When = expressionPointer(literalExpression(true))
			p.Mapping.Directions[DecodeRequest] = mapping
		},
		func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Program.Then.Fields["reasoning"].Fields["historyRetention"] = literalExpression("other")
		},
		func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Program.Otherwise.Fields["parameters"].Fields["wire:claude"] = literalExpression(map[string]any{})
		},
		func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Effects[1].ValueMapping.Unmatched = "/native/value"
		},
	} {
		document := historyPrototype(t, "anthropic")
		change(&document)
		compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
		value := fixtureValue(t, document)
		if compiled, issues := compiler.CompilePackage(value.Bytes()); compiled != nil || IssuesError(issues) == nil {
			t.Fatal("unproved history normalization compiled")
		}
	}
}
