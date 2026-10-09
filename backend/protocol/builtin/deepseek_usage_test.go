package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestDeepSeekPromptCacheAliases(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	for _, raw := range []string{
		`{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":51,"prompt_tokens_details":{"cached_tokens":0}}`,
		`{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":41}`,
	} {
		body := `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + raw + `}`
		r, err := chat.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := chat.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		for _, name := range []string{Responses, Anthropic, Gemini} {
			target := shippedProjectionProtocol(t, name)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(target, chat))
			projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(name, err)
			}
			back, err := target.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
			if err != nil || back.Usage.Input.Count != 51 || back.Usage.CacheRead.Count != r.Usage.CacheRead.Count {
				t.Fatal(name, string(wire), err)
			}
		}
	}
	adapter := module{name: Chat}
	for _, bad := range []string{
		`{"prompt_cache_hit_tokens":null}`, `{"prompt_cache_miss_tokens":-1}`, `{"prompt_cache_miss_tokens":"1"}`, `{"prompt_cache_hit_tokens":1.5}`,
		`{"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":3}`,
		`{"prompt_tokens":5,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":4}`,
	} {
		if _, err := adapter.decodeUsage(testValue(t, bad)); err == nil {
			t.Fatal("invalid cache counts accepted", bad)
		}
	}
}

func TestDeepSeekStreamUsageAliasesAreNotOpaque(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, raw := range []string{
		`{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":null}]}`,
		`{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":51,"completion_tokens":15,"total_tokens":66,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":51,"prompt_tokens_details":{"cached_tokens":0}}}`,
	} {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		for i, event := range frame.Events {
			frame.Events[i], err = c.Event(t.Context(), event, p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = to.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options); err != nil {
			t.Fatal(err)
		}
	}
}
