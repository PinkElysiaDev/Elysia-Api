package builtin

import (
	"bytes"
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
				oldResponse.SchemaVersion = legacy.CoreVersion()
				oldResponse.Native, newResponse.Native = nil, nil
				oldWire, oldErr := legacy.EncodeResponse(t.Context(), oldResponse, p.EvaluationContext{})
				newWire, newErr := draft.EncodeResponse(t.Context(), newResponse, p.EvaluationContext{})
				if oldErr != nil || newErr != nil || !bytes.Equal(oldWire, newWire) {
					t.Fatalf("hosted-tool ledger reconstruction changed: old=%s new=%s errors=%v / %v", oldWire, newWire, oldErr, newErr)
				}
			}
		})
	}
	for _, usage := range []string{
		`42`, `[]`, `{"web_search":null}`, `{"web_search":{"num_requests":null}}`,
		`{"web_search":{"num_requests":-1}}`, `{"web_search":{"num_requests":1.5}}`,
		`{"web_search":{"num_requests":9223372036854775808}}`,
		`{"image_gen":{"input_tokens":2,"output_tokens":3,"total_tokens":4}}`,
		`{"image_gen":{"input_tokens":1,"input_tokens_details":{"image_tokens":2}}}`,
	} {
		t.Run("invalid/"+usage, func(t *testing.T) {
			raw := []byte(hostedUsageResponse(usage))
			_, oldErr := legacy.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			_, newErr := draft.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			var oldFailure, newFailure *p.ConversionError
			if !errors.As(oldErr, &oldFailure) || !errors.As(newErr, &newFailure) || len(oldFailure.Issues) != 1 || len(newFailure.Issues) != 1 {
				t.Fatal("invalid hosted-tool ledger acceptance changed", oldErr, newErr)
			}
			before, after := oldFailure.Issues[0], newFailure.Issues[0]
			if before.Code != after.Code || before.Direction != after.Direction || before.Stage != after.Stage || before.Path != after.Path || before.Reason != after.Reason {
				t.Fatal("hosted-tool rejection diagnosis changed", before, after)
			}
		})
	}
}
