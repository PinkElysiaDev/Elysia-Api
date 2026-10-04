package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The canonical cached/cache_write counters own their detail spellings. A
// mapping expression that writes the detail form would otherwise overwrite the
// count derived from the counters, leaving the two silently disagreeing.
func TestUsageReservedDetailNamesAreRejected(t *testing.T) {
	chat := module{name: Chat, family: "openai_chat"}
	for _, name := range []string{"input.cached_tokens", "input.cache_write_tokens"} {
		usage := &p.Usage{
			Input:   &p.Counter{Count: 100, Origin: p.ObservedCount},
			Details: map[string]p.Counter{name: {Count: 1, Origin: p.ObservedCount}},
		}
		_, err := chat.encodeUsage(usage, p.EvaluationContext{})
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("%s must not shadow the canonical counter: %v", name, err)
		}
	}
	// A non-reserved input detail still crosses as a nested target field.
	usage := &p.Usage{
		Input:   &p.Counter{Count: 100, Origin: p.ObservedCount},
		Details: map[string]p.Counter{"input.text_tokens": {Count: 7, Origin: p.ObservedCount}},
	}
	if _, err := chat.encodeUsage(usage, p.EvaluationContext{}); err != nil {
		t.Fatalf("unrelated input detail must still render: %v", err)
	}
}

// One wire field carries one cache key, so a second intent of the same kind
// cannot be expressed. The breakpoint branch already refused this; the key and
// retention branches silently kept the last value.
func TestCacheKeyIntentDuplicatesAreRejected(t *testing.T) {
	compiled := testCompiled(t, Chat)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"k"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	request.Cache = append(request.Cache, p.CacheIntent{Kind: "key", Location: "request", Value: p.StringValue("second")})
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err == nil {
		t.Fatal("a second key intent must be rejected, not silently overwrite the first")
	}
	// Distinct kinds share no wire field and remain expressible.
	request.Cache = append(request.Cache, p.CacheIntent{Kind: "retention", Location: "request", Value: p.StringValue("24h")})
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err == nil {
		t.Fatal("two key intents must still be rejected")
	}
}
