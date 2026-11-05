package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageFailureEnvelopes(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Anthropic), compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []struct{ name, wire, status string }{
		{"rate-limit", `{"type":"error","error":{"type":"rate_limit_error","message":"quota"}}`, "429"},
		{"authentication", `{"type":"error","error":{"type":"authentication_error","message":"credentials"}}`, "401"},
		{"permission", `{"error":{"type":"permission_error","message":"denied"}}`, "403"},
		{"missing-model", `{"error":{"type":"not_found_error","message":"missing"}}`, "404"},
		{"overloaded", `{"error":{"type":"overloaded_error","message":"busy"}}`, "503"},
		{"nullable-details", `{"error":{"type":"api_error","message":"failed","code":null,"param":null}}`, "500"},
		{"redundant-code", `{"error":{"type":"rate_limit_error","code":"rate_limit_error","message":"quota"}}`, "429"},
		{"string", `{"error":"failed"}`, "500"},
		{"escaped-string", `{"error":"f\u0061iled<&\/next"}`, "500"},
		{"unknown", `{"error":{"type":"vendor","message":"failed","code":"detail","trace":{"n":9007199254740993}},"vendor":null}`, "403"},
		{"opaque-code-param", `{"error":{"type":"api_error","code":false,"param":{"n":1},"message":"failed"}}`, "500"},
		{"without-status", `{"error":{"message":"failed"}}`, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) { checkPackageFailureEnvelope(t, legacy, draft, fixture.wire, fixture.status) })
	}
}

func TestGeminiPackageFailureEnvelopes(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Gemini), compileBuiltinPackage(t, "google-generate-content")
	for _, fixture := range []struct{ name, wire, status string }{
		{"rate-limit", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`, "429"},
		{"status-alias", `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"input"}}`, "400"},
		{"code-mismatch", `{"error":{"code":500,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`, "429"},
		{"missing-status", `{"error":{"code":429,"message":"quota"}}`, "429"},
		{"details", `{"error":{"code":503,"status":"UNAVAILABLE","message":"busy","details":[{"count":9007199254740993}]}}`, "503"},
		{"null-details", `{"error":{"code":500,"status":"INTERNAL","message":"failed","details":null}}`, "500"},
		{"string", `{"error":"failed"}`, "500"},
		{"escaped-string", `{"error":"f\u0061iled<&\/next"}`, "500"},
		{"unknown", `{"error":{"status":"VENDOR","code":9007199254740993,"message":"failed","trace":{"scale":1e2}},"responseId":"r","modelVersion":"m"}`, "403"},
		{"without-context", `{"error":{"code":500,"message":"failed"}}`, ""},
		{"opaque-code", `{"error":{"code":false,"status":"INTERNAL","message":"failed"}}`, "500"},
	} {
		t.Run(fixture.name, func(t *testing.T) { checkPackageFailureEnvelope(t, legacy, draft, fixture.wire, fixture.status) })
	}
}

func TestBlockPackageFailureRejections(t *testing.T) {
	for _, target := range []struct{ codec, packageName, tag string }{{Anthropic, "anthropic", "type"}, {Gemini, "google-generate-content", "status"}} {
		t.Run(target.codec, func(t *testing.T) {
			legacy, draft := shippedProjectionProtocol(t, target.codec), compileBuiltinPackage(t, target.packageName)
			raw := []byte(`{"error":{"` + target.tag + `":null}}`)
			_, oldErr := legacy.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			_, newErr := draft.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			checkPackageFailureRejection(t, oldErr, newErr)
			for _, failure := range []string{
				`{"message":"failed","code":"vendor","param":"detail"}`,
				`{"message":"failed","param":"detail"}`,
				`{"message":"failed","providerType":"new_api_error","code":"vendor"}`,
				`{"message":"failed","providerType":"new_api_error","wire:openai_chat":{"trace":{"n":9007199254740993}}}`,
			} {
				t.Run(failure, func(t *testing.T) {
					response := &p.Response{SchemaVersion: legacy.CoreVersion(), Content: []p.Node{}, Error: testValue(t, failure)}
					_, oldErr := legacy.EncodeResponse(t.Context(), response, p.EvaluationContext{})
					response.SchemaVersion = draft.CoreVersion()
					_, newErr := draft.EncodeResponse(t.Context(), response, p.EvaluationContext{})
					checkPackageFailureRejection(t, oldErr, newErr)
				})
			}
		})
	}
}
