package builtin

import (
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageCacheHintDispositions(t *testing.T) {
	packages := map[string]*p.Compiled{}
	for _, name := range []string{"anthropic", "google-generate-content", "openai-chat", "openai-responses"} {
		packages[name] = compileBuiltinPackage(t, name)
	}
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct {
		name, source, target, hint string
		hasLoss                    bool
	}{
		{"breakpoint-chat", "anthropic", "openai-chat", `"cache_control":{"type":"ephemeral","ttl":"1h"}`, true},
		{"breakpoint-responses", "anthropic", "openai-responses", `"cache_control":{"type":"ephemeral"}`, true},
		{"breakpoint-gemini", "anthropic", "google-generate-content", `"cache_control":{"type":"ephemeral"}`, true},
		{"key-anthropic", "openai-chat", "anthropic", `"prompt_cache_key":"key"`, true},
		{"retention-gemini", "openai-chat", "google-generate-content", `"prompt_cache_retention":"24h"`, true},
		{"null-breakpoint", "anthropic", "openai-chat", `"cache_control":null`, false},
		{"null-key", "openai-chat", "anthropic", `"prompt_cache_key":null`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, target := packages[fixture.source], packages[fixture.target]
			body := `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],` + fixture.hint + "}"
			request, err := source.DecodeRequest(t.Context(), []byte(body), options)
			if err != nil {
				t.Fatal(err)
			}
			before, err := p.EncodeValue(request)
			if err != nil || len(request.Cache) != 1 {
				t.Fatal("source lost cache intent", request.Cache, err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Scope: options.Scope}
			for _, mode := range []string{"compatible", "strict"} {
				policy := cacheProjectionPolicy(t, source, target, mode)
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Request(t.Context(), request, route, sink)
				if mode == "strict" && fixture.hasLoss {
					if err == nil {
						t.Fatal("strict mode accepted cache loss")
					}
					continue
				}
				if err != nil || len(projected.Cache) != 0 {
					t.Fatal("declared disposition did not remove the hint", err)
				}
				issues := cacheProjectionIssues(sink)
				code := p.ConversionNormalized
				if fixture.hasLoss {
					code = p.ConversionDegraded
				}
				if len(issues) != 1 || issues[0].Code != code || issues[0].Path != "/cache/0" || issues[0].RuleID != "request-projection" || issues[0].PolicyHash != conversion.Hash {
					t.Fatal("cache diagnostic lost disposition evidence", issues)
				}
				wire, err := target.EncodeRequest(t.Context(), projected, options)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := target.DecodeRequest(t.Context(), wire, options)
				if err != nil || len(decoded.Cache) != 0 {
					t.Fatal("encoder restored a disposed hint", string(wire), err)
				}
				for i := range policy.Rules {
					if policy.Rules[i].ID == "request-projection" {
						policy.Rules[i].Enabled = false
					}
				}
				disabled, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				retained, err := disabled.Request(t.Context(), request, route, nil)
				if err != nil || !reflect.DeepEqual(retained.Cache, request.Cache) {
					t.Fatal("disabled rule changed cache", err)
				}
				if _, err := target.EncodeRequest(t.Context(), retained, options); err == nil {
					t.Fatal("encoder repeated disabled cache disposition")
				}
			}
			after, err := p.EncodeValue(request)
			if err != nil || before != after {
				t.Fatal("cache projection mutated the source", err)
			}
		})
	}
}

func TestPackageCachePreservationAndUnknowns(t *testing.T) {
	chat := compileBuiltinPackage(t, "openai-chat")
	responses := compileBuiltinPackage(t, "openai-responses")
	anthropic := compileBuiltinPackage(t, "anthropic")
	gemini := compileBuiltinPackage(t, "google-generate-content")
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	for _, fixture := range []struct {
		name, body     string
		source, target *p.Compiled
	}{
		{"breakpoint-native", `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"cache_control":{"type":"ephemeral","ttl":"1h"}}`, anthropic, anthropic},
		{"key-retention-cross", `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"key","prompt_cache_retention":"24h"}`, chat, responses},
		{"resource-native", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"cachedContent":"cachedContents/fixture"}`, gemini, gemini},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request, err := fixture.source.DecodeRequest(t.Context(), []byte(fixture.body), options)
			if err != nil {
				t.Fatal(err)
			}
			conversion, err := p.ResolveConversion(cacheProjectionPolicy(t, fixture.source, fixture.target, "strict"))
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: fixture.source.Identity(), Target: fixture.target.Identity(), Scope: options.Scope}
			for _, rebuild := range []bool{false, true} {
				input := request.Clone()
				if rebuild {
					input.Native = nil
				}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Request(t.Context(), input, route, sink)
				if err != nil || !reflect.DeepEqual(projected.Cache, request.Cache) || len(cacheProjectionIssues(sink)) != 0 {
					t.Fatal("supported cache intent changed", err, sink.Issues())
				}
				wire, err := fixture.target.EncodeRequest(t.Context(), projected, options)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := fixture.target.DecodeRequest(t.Context(), wire, options)
				if err != nil || !reflect.DeepEqual(decoded.Cache, request.Cache) {
					t.Fatal("cache reconstruction lost value, TTL or resource", string(wire), err)
				}
			}
		})
	}
	for _, cache := range []p.CacheIntent{
		{Kind: "breakpoint", Location: "request", Value: testValue(t, `{"type":"ephemeral","future":true}`)},
		{Kind: "breakpoint", Location: "request", Value: testValue(t, `{"type":"ephemeral"}`), TTL: p.StringValue("2h")},
		{Kind: "retention", Location: "request", Value: p.StringValue("future")},
	} {
		request, err := chat.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`), options)
		if err != nil {
			t.Fatal(err)
		}
		request.Cache = []p.CacheIntent{cache}
		conversion, err := p.ResolveConversion(cacheProjectionPolicy(t, chat, gemini, "compatible"))
		if err != nil {
			t.Fatal(err)
		}
		sink := &p.DiagnosticSink{}
		projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: chat.Identity(), Target: gemini.Identity(), Scope: options.Scope}, sink)
		if err != nil || !reflect.DeepEqual(projected.Cache, request.Cache) || len(cacheProjectionIssues(sink)) != 0 {
			t.Fatal("undeclared cache policy was omitted", cache, err)
		}
		if _, err := gemini.EncodeRequest(t.Context(), projected, options); err == nil {
			t.Fatal("target accepted an undeclared cache policy", cache)
		}
	}
}

func TestPackageCacheObjectEdits(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	const raw = `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"cache_control":{"type":"ephemeral","ttl":"1h","future":{"value":9007199254740993}}}`
	request, err := compiled.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		input := request.Clone()
		if rebuild {
			input.Native = nil
		}
		input.Cache[0].TTL = p.Value{}
		output, err := compiled.EncodeRequest(t.Context(), input, p.EvaluationContext{})
		if err != nil {
			t.Fatal("cache edit failed", rebuild, err)
		}
		var wire struct {
			Cache p.Value `json:"cache_control"`
		}
		if err := testValue(t, string(output)).Decode(&wire); err != nil || !equivalentJSON(wire.Cache, testValue(t, `{"type":"ephemeral","future":{"value":9007199254740993}}`)) {
			t.Fatal("cache reconstruction revived TTL or changed its remainder", rebuild, string(output), err)
		}
	}
	request.Native = nil
	request.Parameters["wire:claude"] = testValue(t, `{"cache_control":{"type":"ephemeral","ttl":"5m"}}`)
	if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
		t.Fatal("wire extension replaced the declared cache owner")
	}
}

func cacheProjectionPolicy(t *testing.T, source, target *p.Compiled, mode string) p.ConversionPolicy {
	t.Helper()
	policy := p.DefaultConversionPolicy(source, target)
	var rules []p.ConversionRule
	hasProjection := false
	for _, rule := range policy.Rules {
		if rule.ID == "request-projection" || rule.ID == "responses-storage" {
			rules = append(rules, rule)
			hasProjection = hasProjection || rule.ID == "request-projection"
		}
	}
	if !hasProjection {
		t.Fatal("missing declared cache projection")
	}
	policy.Rules, policy.Mode = rules, mode
	return policy
}

func cacheProjectionIssues(sink *p.DiagnosticSink) []p.ConversionIssue {
	var issues []p.ConversionIssue
	for _, issue := range sink.Issues() {
		if issue.RuleID == "request-projection" {
			issues = append(issues, issue)
		}
	}
	return issues
}
