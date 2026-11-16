package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestUsageReservedDetailNamesAreRejected(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, name := range []string{"input.cached_tokens", "input.cache_write_tokens", "input.cached_creation_tokens"} {
		usage := &p.Usage{Input: &p.Counter{Count: 100, Origin: p.ObservedCount}, Details: map[string]p.Counter{name: {Count: 1, Origin: p.ObservedCount}}}
		_, err := compiled.EncodeResponse(t.Context(), usageProjectionResponse(usage), fixtureOptions(p.EvaluationContext{}))
		if err == nil || !strings.Contains(err.Error(), "/usage/details") {
			t.Fatal("reserved detail overwrote a canonical counter", name, err)
		}
	}
	usage := &p.Usage{Input: &p.Counter{Count: 100, Origin: p.ObservedCount}, Details: map[string]p.Counter{"input.text_tokens": {Count: 7, Origin: p.ObservedCount}}}
	wire, err := compiled.EncodeResponse(t.Context(), usageProjectionResponse(usage), fixtureOptions(p.EvaluationContext{}))
	if err != nil || !strings.Contains(string(wire), `"text_tokens":7`) {
		t.Fatal("unrelated input detail did not render", string(wire), err)
	}
}

func TestCacheKeyIntentDuplicatesAreRejected(t *testing.T) {
	compiled := testCompiled(t, Chat)
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}})
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ kind, value string }{
		{"key", `"k"`}, {"retention", `"24h"`}, {"mode", `"explicit"`}, {"options.ttl", `"5m"`},
	} {
		t.Run(fixture.kind, func(t *testing.T) {
			intent := p.CacheIntent{Kind: fixture.kind, Location: "request", Value: testValue(t, fixture.value)}
			request.Cache = []p.CacheIntent{intent}
			if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
				t.Fatal("single cache intent was rejected", err)
			}
			request.Cache = append(request.Cache, intent)
			before, _ := p.EncodeValue(request)
			_, err := compiled.EncodeRequest(t.Context(), request, options)
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Path != "/cache" || failure.Issues[0].Code != p.UnsupportedCapability {
				t.Fatal("duplicate intent lost rejection or location", err)
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("rejected encoding modified the source request")
			}
		})
	}
	request.Cache = []p.CacheIntent{
		{Kind: "key", Location: "request", Value: p.StringValue("k")},
		{Kind: "retention", Location: "request", Value: p.StringValue("24h")},
	}
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatal("distinct cache intents collided", err)
	}
}
