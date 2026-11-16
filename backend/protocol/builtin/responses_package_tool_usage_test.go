package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageToolUsage(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, fixture := range []struct{ name, usage string }{
		{"null", `null`}, {"empty", `{}`},
		{"empty-containers", `{"image_gen":{"input_tokens_details":{}},"web_search":{}}`},
		{"all-counters", hostedUsageFixture},
		{"unknown", `{"vendor":{"value":9007199254740993}}`},
		{"partial-with-unknown", `{"image_gen":{"input_tokens":7,"vendor":[null,9007199254740993]},"web_search":{"num_requests":0}}`},
		{"partial-with-empty", `{"image_gen":{},"web_search":{"num_requests":2}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, hasModelUsage := range []bool{true, false} {
				raw := hostedUsageResponse(fixture.usage)
				if !hasModelUsage {
					raw = strings.Replace(raw, `,"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}`, "", 1)
				}
				response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal("valid hosted usage was rejected", err)
				}
				if hasModelUsage && (response.Usage == nil || response.Usage.Input.Count != 3 || response.Usage.Output.Count != 1 || response.Usage.Total.Count != 4) {
					t.Fatal("hosted ledger changed model totals", response.Usage)
				}
				if !hasModelUsage && response.Usage != nil && (response.Usage.Input != nil || response.Usage.Output != nil || response.Usage.Total != nil) {
					t.Fatal("hosted ledger invented model counters", response.Usage)
				}
				response.Native = nil
				wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				expected := strings.Replace(raw, `"id":"r"`, `"id":"r","object":"response"`, 1)
				expected = strings.Replace(expected, `"text":"OK"`, `"text":"OK","annotations":[]`, 1)
				sameJSON(t, wire, expected)
			}
		})
	}
	for _, fixture := range []struct{ usage, path string }{
		{`42`, "/tool_usage"}, {`[]`, "/tool_usage"}, {`{"web_search":null}`, "/tool_usage/web_search"},
		{`{"web_search":{"num_requests":null}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":-1}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":1.5}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":9223372036854775808}}`, "/tool_usage/web_search/num_requests"},
		{`{"image_gen":{"input_tokens":2,"output_tokens":3,"total_tokens":4}}`, "/tool_usage/image_gen/total_tokens"},
		{`{"image_gen":{"input_tokens":1,"input_tokens_details":{"image_tokens":2}}}`, "/tool_usage/image_gen/input_tokens"},
	} {
		_, err := compiled.DecodeResponse(t.Context(), []byte(hostedUsageResponse(fixture.usage)), fixtureOptions(p.EvaluationContext{}))
		var failure *p.ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != fixture.path || failure.Issues[0].Direction != p.DecodeResponse || failure.Issues[0].Protocol != compiled.Identity() {
			t.Fatal("invalid hosted ledger lost its source rejection", fixture.usage, err)
		}
	}
}
