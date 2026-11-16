package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageFailureEnvelopes(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []failureEnvelopeCase{
		{name: "rate-limit", wire: `{"type":"error","error":{"type":"rate_limit_error","message":"quota"}}`, status: "429"},
		{name: "authentication", wire: `{"type":"error","error":{"type":"authentication_error","message":"credentials"}}`, status: "401"},
		{name: "permission", wire: `{"error":{"type":"permission_error","message":"denied"}}`, status: "403"},
		{name: "missing-model", wire: `{"error":{"type":"not_found_error","message":"missing"}}`, status: "404"},
		{name: "overloaded", wire: `{"error":{"type":"overloaded_error","message":"busy"}}`, status: "503"},
		{name: "nullable-details", wire: `{"error":{"type":"api_error","message":"failed","code":null,"param":null}}`, status: "500"},
		{name: "redundant-code", wire: `{"error":{"type":"rate_limit_error","code":"rate_limit_error","message":"quota"}}`, status: "429"},
		{name: "string", wire: `{"error":"failed"}`, status: "500", rebuiltError: `{"category":"upstream","message":"failed"}`},
		{name: "escaped-string", wire: `{"error":"f\u0061iled<&\/next"}`, status: "500", rebuiltError: `{"category":"upstream","message":"failed<&/next"}`},
		{name: "unknown", wire: `{"error":{"type":"vendor","message":"failed","code":"detail","trace":{"n":9007199254740993}},"vendor":null}`, status: "403"},
		{name: "opaque-code-param", wire: `{"error":{"type":"api_error","code":false,"param":{"n":1},"message":"failed"}}`, status: "500"},
		{name: "without-status", wire: `{"error":{"message":"failed"}}`, status: "", rebuiltError: `{"category":"upstream","message":"failed"}`},
	} {
		t.Run(fixture.name, func(t *testing.T) { checkPackageFailureEnvelope(t, compiled, fixture) })
	}
}

func TestGeminiPackageFailureEnvelopes(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	for _, fixture := range []failureEnvelopeCase{
		{name: "rate-limit", wire: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`, status: "429"},
		{name: "status-alias", wire: `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"input"}}`, status: "400"},
		{name: "code-mismatch", wire: `{"error":{"code":500,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`, status: "429"},
		{name: "missing-status", wire: `{"error":{"code":429,"message":"quota"}}`, status: "429", rebuiltError: `{"category":"rate_limit","message":"quota"}`},
		{name: "details", wire: `{"error":{"code":503,"status":"UNAVAILABLE","message":"busy","details":[{"count":9007199254740993}]}}`, status: "503"},
		{name: "null-details", wire: `{"error":{"code":500,"status":"INTERNAL","message":"failed","details":null}}`, status: "500"},
		{name: "string", wire: `{"error":"failed"}`, status: "500", rebuiltError: `{"category":"overloaded","message":"failed","wire:gemini":{"code":500}}`},
		{name: "escaped-string", wire: `{"error":"f\u0061iled<&\/next"}`, status: "500", rebuiltError: `{"category":"overloaded","message":"failed<&/next","wire:gemini":{"code":500}}`},
		{name: "unknown", wire: `{"error":{"status":"VENDOR","code":9007199254740993,"message":"failed","trace":{"scale":1e2}},"responseId":"r","modelVersion":"m"}`, status: "403"},
		{name: "without-context", wire: `{"error":{"code":500,"message":"failed"}}`, status: "", rebuiltError: `{"category":"overloaded","message":"failed","wire:gemini":{"code":500}}`},
		{name: "opaque-code", wire: `{"error":{"code":false,"status":"INTERNAL","message":"failed"}}`, status: "500"},
	} {
		t.Run(fixture.name, func(t *testing.T) { checkPackageFailureEnvelope(t, compiled, fixture) })
	}
}

func TestBlockPackageFailureRejections(t *testing.T) {
	for _, target := range []struct{ codec, tag string }{{Anthropic, "type"}, {Gemini, "status"}} {
		t.Run(target.codec, func(t *testing.T) {
			compiled := testCompiled(t, target.codec)
			raw := []byte(`{"error":{"` + target.tag + `":null}}`)
			_, err := compiled.DecodeResponse(t.Context(), raw, fixtureOptions(p.EvaluationContext{}))
			requireFailureRejection(t, err)
			for _, failure := range []string{
				`{"message":"failed","code":"vendor","param":"detail"}`,
				`{"message":"failed","param":"detail"}`,
				`{"message":"failed","providerType":"new_api_error","code":"vendor"}`,
				`{"message":"failed","providerType":"new_api_error","wire:openai_chat":{"trace":{"n":9007199254740993}}}`,
			} {
				t.Run(failure, func(t *testing.T) {
					response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{}, Error: testValue(t, failure)}
					_, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
					requireFailureRejection(t, err)
				})
			}
		})
	}
}

func TestFailureEnvelopeRetainsForeignLookingFields(t *testing.T) {
	for _, sample := range []struct{ name, error, remainder string }{
		{Chat, `{"type":"rate_limit_error","message":"quota"}`, `"output":42,"content":false,"candidates":{"opaque":9007199254740993}`},
		{Responses, `{"type":"rate_limit_error","message":"quota"}`, `"choices":42,"content":false,"candidates":{"opaque":9007199254740993}`},
		{Anthropic, `{"type":"rate_limit_error","message":"quota"}`, `"choices":42,"output":false,"candidates":{"opaque":9007199254740993}`},
		{Gemini, `{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}`, `"choices":42,"output":false,"content":{"opaque":9007199254740993}`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			checkPackageFailureEnvelope(t, testCompiled(t, sample.name), failureEnvelopeCase{wire: `{"error":` + sample.error + `,` + sample.remainder + `}`, status: "429"})
		})
	}
}
