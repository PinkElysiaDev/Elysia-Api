package builtin

import (
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicUsageCounterFirstErrorOrder(t *testing.T) {
	const (
		counterReason     = "usage counter must be a nonnegative int64"
		requiredReason    = "known execution iteration requires a nonnegative int64 counter"
		detailReason      = "usage details must be an object"
		iterationCounters = `"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0`
	)
	iterationUsage := func(fields string) string {
		return `{"input_tokens":10,"output_tokens":2,"iterations":[{` + fields + `}]}`
	}
	compiled := testCompiled(t, Anthropic)
	for _, testCase := range []struct {
		name, usage, path, reason string
	}{
		{"root-shape-before-input", `{"input_tokens":-1,"output_tokens":2,"cache_creation":false,"output_tokens_details":[]}`, "/usage/cache_creation", detailReason},
		{"root-output-shape-before-input", `{"input_tokens":-1,"output_tokens":2,"output_tokens_details":false}`, "/usage/output_tokens_details", detailReason},
		{"root-input-before-total", `{"input_tokens":-1,"output_tokens":-2,"total_tokens":-3}`, "/usage/input_tokens", counterReason},
		{"root-total-before-cache", `{"input_tokens":10,"output_tokens":2,"total_tokens":-1,"cache_read_input_tokens":-2}`, "/usage/total_tokens", counterReason},
		{"array-shape-before-entry-counter", `{"input_tokens":10,"output_tokens":2,"iterations":[{"type":"message"},false]}`, "/usage/iterations", "iterations must be an ordered array or null"},
		{"required-input-before-detail", iterationUsage(`"type":"message","output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":false`), "/usage/iterations/0/input_tokens", requiredReason},
		{"required-output-before-cache", iterationUsage(`"type":"message","input_tokens":1,"output_tokens":-1,"cache_read_input_tokens":-2,"cache_creation_input_tokens":0`), "/usage/iterations/0/output_tokens", requiredReason},
		{"required-cache-before-detail", iterationUsage(`"type":"message","input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":0,"cache_creation":false`), "/usage/iterations/0/cache_read_input_tokens", requiredReason},
		{"iteration-detail-before-total", iterationUsage(`"type":"message",` + iterationCounters + `,"cache_creation":false,"total_tokens":-1`), "/usage/iterations/0/cache_creation", detailReason},
		{"iteration-total-before-model", iterationUsage(`"type":"advisor_message",` + iterationCounters + `,"total_tokens":-1`), "/usage/iterations/0/total_tokens", counterReason},
		{"retention-before-thinking", iterationUsage(`"type":"message",` + iterationCounters + `,"cache_creation":{"ephemeral_5m_input_tokens":-1,"ephemeral_1h_input_tokens":-2},"output_tokens_details":{"thinking_tokens":-3}`), "/usage/iterations/0/cache_creation/ephemeral_5m_input_tokens", counterReason},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(testCase.usage)), fixtureOptions(p.EvaluationContext{}))
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatalf("expected one ordered Spec rejection, got %v", err)
			}
			issue := failure.Issues[0]
			if issue.Code != p.UpstreamContractViolation || issue.Path != testCase.path || issue.Reason != testCase.reason {
				t.Fatalf("first rejection changed: %+v", issue)
			}
		})
	}
}

func TestAnthropicUsageCounterOptionalAndUnknownBoundaries(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, usage := range []string{
		`null`,
		`{"input_tokens":10,"output_tokens":2,"cache_creation":null,"output_tokens_details":null}`,
		`{"input_tokens":10,"output_tokens":2,"iterations":null}`,
		`{"input_tokens":10,"output_tokens":2,"iterations":[]}`,
		`{"input_tokens":10,"output_tokens":2,"iterations":[{"type":"future","input_tokens":false,"total_tokens":-1,"cache_creation":false,"output_tokens_details":[],"model":[]}]}`,
	} {
		if _, err := compiled.DecodeResponse(t.Context(), []byte(buildExecutionResponse(usage)), fixtureOptions(p.EvaluationContext{})); err != nil {
			t.Fatal("optional or source-owned observation was reinterpreted", usage, err)
		}
	}
}
