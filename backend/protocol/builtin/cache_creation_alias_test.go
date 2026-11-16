package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func cacheAliasResponse(name, usage string) string {
	if name == Chat {
		return `{"id":"r","model":"m","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + usage + `}`
	}
	return `{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[],"usage":` + usage + `}`
}

func TestCacheAliasesRequireOwnWireProvenance(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	const body = `{"id":"r","model":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":1,"cache_creation_input_tokens":7}}`
	response, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			target := shippedProjectionProtocol(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(from, target))
			if err != nil {
				t.Fatal(err)
			}
			route := fixtureConversionContext(t, conversion, p.ConversionResponse, from.Identity())
			route.Transport = p.HTTPJSON
			projected, err := conversion.Response(t.Context(), response, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), "cache_creation_input_tokens") || !strings.Contains(string(wire), `"cache_write_tokens":7`) {
				t.Fatal("foreign wire selected a target alias or lost the canonical count", string(wire))
			}
		})
	}
}

func TestCacheCreationAliasValidationAndNativeMutation(t *testing.T) {
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			compiled := testCompiled(t, name)
			key := "input_tokens_details"
			if name == Chat {
				key = "prompt_tokens_details"
			}
			decode := func(fields string) (*p.Response, error) {
				usage := fmt.Sprintf(`{"%s":{%s}}`, key, fields)
				return compiled.DecodeResponse(t.Context(), []byte(cacheAliasResponse(name, usage)), fixtureOptions(p.EvaluationContext{}))
			}
			for _, count := range []int64{0, 30, 9007199254740993} {
				response, err := decode(fmt.Sprintf(`"cached_creation_tokens":%d,"cache_write_tokens":%d`, count, count))
				if err != nil || response.Usage == nil || response.Usage.CacheCreation == nil || response.Usage.CacheCreation.Count != count {
					t.Fatal("aliases were not decoded as one exact count", response, err)
				}
				wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				fields := fmt.Sprintf(`{"%s":{"cached_creation_tokens":%d,"cache_write_tokens":%d}}`, key, count, count)
				sameJSON(t, wire, cacheAliasResponse(name, fields))
			}
			for _, fields := range []string{`"cached_creation_tokens":null`, `"cached_creation_tokens":-1`, `"cached_creation_tokens":"0"`, `"cached_creation_tokens":1.5`, `"cached_creation_tokens":1,"cache_write_tokens":2`} {
				if _, err := decode(fields); err == nil {
					t.Fatal("invalid or conflicting alias accepted", fields)
				}
			}
			response, err := decode(`"cached_creation_tokens":30`)
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range []*p.Counter{{Count: 31, Origin: p.ObservedCount}, nil} {
				response.Usage.CacheCreation = count
				wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				updated, err := compiled.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(string(wire), err)
				}
				if count == nil && updated.Usage != nil && updated.Usage.CacheCreation != nil || count != nil && (updated.Usage == nil || updated.Usage.CacheCreation == nil || updated.Usage.CacheCreation.Count != count.Count) {
					t.Fatal("native alias overrode the core edit", string(wire))
				}
			}
		})
	}
}
