package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestStandardCacheWriteCountersPreservePresenceAndRejectConflicts(t *testing.T) {
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			adapter := module{name: name}
			input, details := "input_tokens", "input_tokens_details"
			if name == Chat {
				input, details = "prompt_tokens", "prompt_tokens_details"
			}
			for _, fixture := range []struct {
				write string
				want  int64
			}{{``, -1}, {`,"cache_write_tokens":0`, 0}, {`,"cache_write_tokens":30`, 30}} {
				raw := fmt.Sprintf(`{"%s":100,"%s":{"cached_tokens":70%s}}`, input, details, fixture.write)
				usage, err := adapter.decodeUsage(testValue(t, raw))
				if err != nil {
					t.Fatal(err)
				}
				if usage.Input.Count != 100 {
					t.Fatal("input was double counted")
				}
				if fixture.want < 0 {
					if usage.CacheCreation != nil {
						t.Fatal("missing creation became zero")
					}
					continue
				}
				if usage.CacheCreation == nil || usage.CacheCreation.Count != fixture.want {
					t.Fatalf("creation lost: %s -> %+v", raw, usage)
				}
				encoded, err := adapter.encodeUsage(usage)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, encoded.Bytes(), raw)
			}
			for _, value := range []string{`-1`, `null`, `"30"`, `1.5`, `9223372036854775808`} {
				if _, err := adapter.decodeUsage(testValue(t, fmt.Sprintf(`{"%s":{"cache_write_tokens":%s}}`, details, value))); err == nil {
					t.Fatalf("invalid counter accepted: %s", value)
				}
			}
			if _, err := adapter.decodeUsage(testValue(t, fmt.Sprintf(`{"cache_creation_input_tokens":2,"%s":{"cache_write_tokens":3}}`, details))); err == nil {
				t.Fatal("conflicting counters accepted")
			}
			for _, raw := range []string{`{"cache_creation_input_tokens":30}`, fmt.Sprintf(`{"cache_creation_input_tokens":30,"%s":{"cache_write_tokens":30}}`, details)} {
				usage, err := adapter.decodeUsage(testValue(t, raw))
				if err != nil || usage.CacheCreation == nil || usage.CacheCreation.Count != 30 {
					t.Fatal("legacy counter rejected or summed", err)
				}
			}
		})
	}
}

func TestCacheWriteLateFrameAndNativeReplay(t *testing.T) {
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			input, details := "input_tokens", "input_tokens_details"
			if name == Chat {
				input, details = "prompt_tokens", "prompt_tokens_details"
			}
			stream := &streamModule{module: module{name: name}, limits: p.DefaultLimits()}
			var merged *p.Usage
			for _, frame := range []string{fmt.Sprintf(`{"%s":100}`, input), fmt.Sprintf(`{"%s":{"cache_write_tokens":30,"cached_tokens":70}}`, details), fmt.Sprintf(`{"%s":{"cache_write_tokens":0}}`, details)} {
				usage, err := stream.decodeUsageUpdate(testValue(t, frame))
				if err != nil {
					t.Fatal(err)
				}
				merged = p.MergeUsage(merged, usage)
			}
			if merged.CacheCreation == nil || merged.CacheCreation.Count != 0 || merged.CacheRead.Count != 70 || merged.Input.Count != 100 {
				t.Fatal("tail presence lost", merged)
			}
			compiled := testCompiled(t, name)
			body := `{"id":"r","status":"completed","output":[],"usage":{"input_tokens":100,"cache_creation_input_tokens":30}}`
			if name == Chat {
				body = `{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"cache_creation_input_tokens":30}}`
			}
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			response.Usage.CacheCreation.Count = 31
			wire, err = compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			updated, err := compiled.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
			if err != nil || updated.Usage.CacheCreation.Count != 31 {
				t.Fatal("native legacy alias overrode semantic mutation", string(wire), err)
			}
			response.Usage.CacheCreation = nil
			wire, err = compiled.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			updated, err = compiled.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
			if err != nil || updated.Usage.CacheCreation != nil {
				t.Fatal("deleted creation resurrected from native legacy alias", string(wire), err)
			}
		})
	}
}
