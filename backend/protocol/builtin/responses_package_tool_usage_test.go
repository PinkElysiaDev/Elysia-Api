package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageToolUsage(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, fixture := range []struct{ name, usage string }{
		{"null", `null`},
		{"empty", `{}`},
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
				oldResponse, oldErr := legacy.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
				newResponse, newErr := draft.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
				if oldErr != nil || newErr != nil {
					t.Fatal("valid hosted-tool ledger was rejected", oldErr, newErr)
				}
				oldResponse.SchemaVersion = draft.CoreVersion()
				want, _ := p.EncodeValue(oldResponse)
				got, _ := p.EncodeValue(newResponse)
				checkPackageMigration(t, want.Bytes(), got.Bytes(), nil, nil, packageMigrationGap{})
				newResponse.Native = nil
				newWire, newErr := draft.EncodeResponse(t.Context(), newResponse, p.EvaluationContext{})
				if newErr != nil {
					t.Fatal(newErr)
				}
				// Required output envelopes are explicit; a tool-only ledger must
				// not manufacture an empty model-usage object.
				expected := strings.Replace(raw, `"id":"r"`, `"id":"r","object":"response"`, 1)
				expected = strings.Replace(expected, `"text":"OK"`, `"text":"OK","annotations":[]`, 1)
				if !equalAgentMigrationValues(t, testValue(t, string(newWire)), testValue(t, expected)) {
					t.Fatalf("reported tool/model ledgers or output changed: expected=%s actual=%s", expected, newWire)
				}
			}
		})
	}
	for _, fixture := range []struct{ usage, path string }{
		{`42`, "/tool_usage"},
		{`[]`, "/tool_usage"},
		{`{"web_search":null}`, "/tool_usage/web_search"},
		{`{"web_search":{"num_requests":null}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":-1}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":1.5}}`, "/tool_usage/web_search/num_requests"},
		{`{"web_search":{"num_requests":9223372036854775808}}`, "/tool_usage/web_search/num_requests"},
		{`{"image_gen":{"input_tokens":2,"output_tokens":3,"total_tokens":4}}`, "/tool_usage/image_gen/total_tokens"},
		{`{"image_gen":{"input_tokens":1,"input_tokens_details":{"image_tokens":2}}}`, "/tool_usage/image_gen/input_tokens"},
	} {
		t.Run("invalid/"+fixture.usage, func(t *testing.T) {
			raw := []byte(hostedUsageResponse(fixture.usage))
			_, err := draft.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal("invalid hosted-tool ledger lost its structured rejection", err)
			}
			issue := failure.Issues[0]
			if issue.Code != p.InvalidInput || issue.Severity != p.SeverityError || issue.Protocol != draft.Identity() || issue.Direction != p.DecodeResponse || issue.Stage != "wire" || issue.Reason == "" || issue.Path != fixture.path {
				t.Fatal("hosted-tool rejection lost its source attribution", issue)
			}
		})
	}
}
