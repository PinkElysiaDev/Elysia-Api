package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicNullableCacheDetailsPreserveObservedUsage(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, details := range []string{
		`"cache_creation":null`,
		`"cache_creation":null,"output_tokens_details":null`,
		`"cache_creation":null,"cache_creation_input_tokens":0`,
	} {
		usage := `{"input_tokens":10,"output_tokens":2,` + details + `}`
		response, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		if response.Usage.Input.Count != 10 || response.Usage.Output.Count != 2 {
			t.Fatal("null cache details changed parent counters", response.Usage)
		}
		response.Native = nil
		checkEncodedExecutionUsage(t, compiled, response, usage)
	}
}
