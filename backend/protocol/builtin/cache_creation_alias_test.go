package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// new-api's Anthropic adapter emits both cached_creation_tokens and
// cache_write_tokens for the same count. They are aliases, never additive.
func TestCacheCreationAliasValidationAndNativeMutation(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		t.Run(codec, func(t *testing.T) {
			key := "input_tokens_details"
			if codec == Chat {
				key = "prompt_tokens_details"
			}
			adapter := module{name: codec}
			for _, count := range []int64{0, 30, 9007199254740993} {
				wire := fmt.Sprintf(`{"%s":{"cached_creation_tokens":%d,"cache_write_tokens":%d}}`, key, count, count)
				u, err := adapter.decodeUsage(testValue(t, wire))
				if err != nil || u.CacheCreation == nil || u.CacheCreation.Count != count {
					t.Fatalf("count: %+v %v", u, err)
				}
				if extra, err := adapter.usageExtensions(testValue(t, wire)); err != nil || !extra.IsZero() {
					t.Fatal("recognized alias remains opaque", extra, err)
				}
			}
			for _, fields := range []string{`"cached_creation_tokens":null`, `"cached_creation_tokens":-1`, `"cached_creation_tokens":"0"`, `"cached_creation_tokens":1.5`, `"cached_creation_tokens":1,"cache_write_tokens":2`} {
				if _, err := adapter.decodeUsage(testValue(t, fmt.Sprintf(`{"%s":{%s}}`, key, fields))); err == nil {
					t.Fatal("invalid or conflicting alias accepted", fields)
				}
			}
			compiled := testCompiled(t, codec)
			body := `{"id":"r","output":[],"usage":{"input_tokens_details":{"cached_creation_tokens":30}}}`
			if codec == Chat {
				body = `{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens_details":{"cached_creation_tokens":30}}}`
			}
			r, err := compiled.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeResponse(t.Context(), r, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			for _, count := range []*p.Counter{{Count: 31, Origin: p.ObservedCount}, nil} {
				r.Usage.CacheCreation = count
				wire, err = compiled.EncodeResponse(t.Context(), r, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				updated, err := compiled.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
				if err != nil {
					t.Fatal(string(wire), err)
				}
				if count == nil && updated.Usage.CacheCreation != nil || count != nil && (updated.Usage.CacheCreation == nil || updated.Usage.CacheCreation.Count != count.Count) {
					t.Fatal("native alias overrode mutation", string(wire))
				}
			}
		})
	}
}
