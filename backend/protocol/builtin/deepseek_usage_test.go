package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestDeepSeekStreamUsageAliasesAreNotOpaque(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	frames := convertFixtureStream(t, from, to, []string{
		`{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":null}]}`,
		`{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":51,"prompt_tokens_details":{"cached_tokens":0}}}`,
	})
	var wire []string
	for _, frame := range frames {
		wire = append(wire, frame.JSONString())
	}
	_, response := decodePackageStream(t, to, wire, p.Scope{})
	if response.Usage.Input.Count != 51 || response.Usage.Output.Count != 15 || response.Usage.CacheRead.Count != 0 {
		t.Fatal("usage aliases changed accounting", response.Usage)
	}
}

func TestDeepSeekLateCacheMissUsesRetainedInput(t *testing.T) {
	for _, hit := range []string{`{"prompt_cache_hit_tokens":2}`, `{"prompt_tokens_details":{"cached_tokens":2}}`} {
		from := shippedProjectionProtocol(t, Chat)
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		var usage *p.Usage
		for _, raw := range []string{`{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}`, hit, `{"prompt_cache_miss_tokens":3}`, `{"prompt_cache_miss_tokens":3}`} {
			frame, err := from.DecodeFrame(t.Context(), testValue(t, `{"choices":[],"usage":`+raw+`}`), options)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range frame.Events {
				if err := p.ValidateUsageArithmetic(event.Usage); err != nil {
					t.Fatal(err)
				}
				usage = p.MergeUsage(usage, event.Usage)
			}
		}
		if usage.Input.Count != 5 || usage.Input.Origin != p.ObservedCount || usage.CacheRead.Count != 2 || usage.Details["uncached_input_tokens"].Count != 3 || usage.Total.Count != 6 {
			t.Fatal(usage)
		}
	}
}

func TestDeepSeekPromptCacheAliases(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	for _, raw := range []string{
		`{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":51,"prompt_tokens_details":{"cached_tokens":0}}`,
		`{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":41}`,
	} {
		body := `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + raw + `}`
		r, err := chat.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := chat.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		for _, name := range []string{Responses, Anthropic, Gemini} {
			target := shippedProjectionProtocol(t, name)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(target, chat))
			projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, chat.Identity()), nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(name, err)
			}
			back, err := target.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil || back.Usage.Input.Count != 51 || back.Usage.CacheRead.Count != r.Usage.CacheRead.Count {
				t.Fatal(name, string(wire), err)
			}
		}
	}

	for _, bad := range []string{
		`{"prompt_cache_hit_tokens":null}`, `{"prompt_cache_miss_tokens":-1}`, `{"prompt_cache_miss_tokens":"1"}`, `{"prompt_cache_hit_tokens":1.5}`,
		`{"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":3}`,
		`{"prompt_tokens":5,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":4}`,
	} {
		if _, err := decodeUsageResponse(t, chat, bad); err == nil {
			t.Fatal("invalid cache counts accepted", bad)
		}
	}
}

func TestDeepSeekCacheMissAndNestedHitInferInput(t *testing.T) {
	u, err := decodeUsageResponse(t, testCompiled(t, Chat), `{"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_miss_tokens":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if u.Input == nil || u.Input.Count != 5 || u.Input.Origin != p.InferredCount || u.CacheRead.Origin != p.ObservedCount {
		t.Fatal(u)
	}
	if err := p.ValidateUsageArithmetic(u); err != nil {
		t.Fatal(err)
	}
}
