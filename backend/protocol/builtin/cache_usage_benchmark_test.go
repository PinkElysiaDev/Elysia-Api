package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The legacy counter is understood by both the release baseline and this
// version, so mutation has equivalent semantics in the performance comparison.
func BenchmarkCacheUsageMutation(b *testing.B) {
	compiled := testCompiled(b, Chat)
	body := []byte(`{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":2,"cache_creation_input_tokens":20}}`)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err := compiled.DecodeResponse(b.Context(), body, p.EvaluationContext{})
		if err != nil {
			b.Fatal(err)
		}
		response.Usage.CacheCreation.Count = 19
		if _, err := compiled.EncodeResponse(b.Context(), response, p.EvaluationContext{}); err != nil {
			b.Fatal(err)
		}
	}
}
