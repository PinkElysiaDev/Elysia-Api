package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Keep this wire fixture identical to C01's cacheChatFixture. Only Anthropic
// can express its top-level breakpoints; other targets must reject that intent.
const benchmarkCacheRequest = `{"model":"m","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"system","content":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"developer","content":"stable developer"},{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}},"cache_control":{"type":"ephemeral"}}]}`

func BenchmarkCacheConversion(b *testing.B) {
	source, target := testCompiled(b, Chat), testCompiled(b, Anthropic)
	body := []byte(benchmarkCacheRequest)
	b.ReportAllocs()
	for b.Loop() {
		request, err := source.DecodeRequest(b.Context(), body, p.EvaluationContext{})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := target.EncodeRequest(b.Context(), request, p.EvaluationContext{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamTextDecode(b *testing.B) {
	compiled := testCompiled(b, Chat)
	frame, _ := p.ParseValue([]byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, strings.Repeat("x", 256))))
	finish, _ := p.ParseValue([]byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`))
	b.ReportAllocs()
	b.SetBytes(256 * 256)
	for b.Loop() {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		for range 256 {
			if _, err := compiled.DecodeFrame(b.Context(), frame, options); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := compiled.DecodeFrame(b.Context(), finish, options); err != nil {
			b.Fatal(err)
		}
	}
}
