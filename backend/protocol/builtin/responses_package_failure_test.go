package builtin

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageFailureEnvelopes(t *testing.T) {
	checkOpenAIPackageFailureEnvelopes(t, Responses, "openai-responses")
}

func TestChatPackageFailureEnvelopes(t *testing.T) {
	checkOpenAIPackageFailureEnvelopes(t, Chat, "openai-chat")
}

func checkOpenAIPackageFailureEnvelopes(t *testing.T, codec, packageName string) {
	t.Helper()
	legacy, draft := shippedProjectionProtocol(t, codec), compileBuiltinPackage(t, packageName)
	for _, fixture := range []struct{ name, wire, status string }{
		{"rate-limit", `{"error":{"type":"rate_limit_error","message":"quota","code":null,"param":null}}`, "429"},
		{"authentication", `{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"credentials"}}`, "401"},
		{"missing-model", `{"error":{"type":"invalid_request_error","code":"model_not_found","param":"model","message":"missing"}}`, "404"},
		{"overloaded", `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"busy"}}`, "503"},
		{"server-alias", `{"error":{"type":"server_error","message":"failed"}}`, "500"},
		{"upstream-alias", `{"error":{"type":"upstream_error","code":"upstream_error","message":"failed"}}`, "502"},
		{"wrapper", `{"error":{"type":"new_api_error","code":"vendor_limit","message":"limited"}}`, "403"},
		{"redundant-code", `{"error":{"type":"rate_limit_error","code":"rate_limit_error","message":"quota"}}`, "429"},
		{"string", `{"error":"failed"}`, "500"},
		{"escaped-string", `{"error":"f\u0061iled<&\/next"}`, "500"},
		{"unknown-fields", `{"error":{"message":"limited","type":"vendor_budget","code":"vendor_code","trace":{"n":9007199254740993,"enabled":false,"detail":null}},"vendor":[]}`, "403"},
		{"content-and-error", `{"id":"r","model":"m","status":"failed","output":[],"error":{"type":"api_error","message":"failed"},"usage":{"input_tokens":2,"output_tokens":0}}`, "500"},
		{"without-status", `{"error":{"type":"server_error","message":"failed"}}`, ""},
		{"wrapper-without-status", `{"error":{"type":"new_api_error","message":"failed"}}`, ""},
		{"unclassified-without-status", `{"error":{"message":"failed"}}`, ""},
		{"unclassified-null-status", `{"error":{"message":"failed"}}`, "null"},
		{"envelope-extensions", `{"error":{"type":"rate_limit_error","message":"quota"},"store":"provider-state","tool_usage":42}`, "429"},
	} {
		if codec == Chat && fixture.name == "content-and-error" {
			fixture.wire = `{"id":"r","model":"m","status":"failed","choices":[],"error":{"type":"api_error","message":"failed"},"usage":{"prompt_tokens":2,"completion_tokens":0}}`
		}
		t.Run(fixture.name, func(t *testing.T) {
			checkPackageFailureEnvelope(t, legacy, draft, fixture.wire, fixture.status)
		})
	}
}

func checkPackageFailureEnvelope(t *testing.T, legacy, draft *p.Compiled, wire, status string) {
	t.Helper()
	options := p.EvaluationContext{}
	if status != "" {
		options.Values = p.Object{"httpStatus": testValue(t, status)}
	}
	oldResponse, oldErr := legacy.DecodeResponse(t.Context(), []byte(wire), options)
	newResponse, newErr := draft.DecodeResponse(t.Context(), []byte(wire), options)
	if oldErr != nil || newErr != nil {
		t.Fatal("declared provider failure stopped decoding", oldErr, newErr)
	}
	oldResponse.SchemaVersion = draft.CoreVersion()
	want, _ := p.EncodeValue(oldResponse)
	got, _ := p.EncodeValue(newResponse)
	checkPackageMigration(t, want.Bytes(), got.Bytes(), nil, nil, packageMigrationGap{})
	if p.CheckGenerationOutcome(newResponse) == nil {
		t.Fatal("provider error became a successful response")
	}
	oldResponse.SchemaVersion = legacy.CoreVersion()
	oldNativeWire, oldErr := legacy.EncodeResponse(t.Context(), oldResponse, options)
	newNativeWire, newErr := draft.EncodeResponse(t.Context(), newResponse, options)
	if oldErr != nil || newErr != nil || !bytes.Equal(oldNativeWire, newNativeWire) {
		t.Fatalf("native failure replay changed: old=%s new=%s errors=%v / %v", oldNativeWire, newNativeWire, oldErr, newErr)
	}
	oldFinalErr := legacy.ValidateWireOutput(p.EncodeResponse, testValue(t, string(oldNativeWire)))
	newFinalErr := draft.ValidateWireOutput(p.EncodeResponse, testValue(t, string(newNativeWire)))
	if oldFinalErr != nil || newFinalErr != nil {
		checkPackageFailureRejection(t, oldFinalErr, newFinalErr)
	}
	oldResponse.Native, newResponse.Native = nil, nil
	oldWire, oldErr := legacy.EncodeResponse(t.Context(), oldResponse, options)
	newWire, newErr := draft.EncodeResponse(t.Context(), newResponse, options)
	if oldErr != nil || newErr != nil || !bytes.Equal(oldWire, newWire) {
		t.Fatalf("failure reconstruction changed: old=%s new=%s errors=%v / %v", oldWire, newWire, oldErr, newErr)
	}
}

func TestResponsesPackageFailureContextBoundary(t *testing.T) {
	checkOpenAIPackageFailureContext(t, Responses, "openai-responses")
}

func TestChatPackageFailureContextBoundary(t *testing.T) {
	checkOpenAIPackageFailureContext(t, Chat, "openai-chat")
}

func checkOpenAIPackageFailureContext(t *testing.T, codec, packageName string) {
	t.Helper()
	legacy, draft := shippedProjectionProtocol(t, codec), compileBuiltinPackage(t, packageName)
	options := p.EvaluationContext{Values: p.Object{"httpStatus": p.StringValue("invalid")}}
	for _, wire := range []string{`{"error":{"message":"failure"}}`, `{"error":{"type":"rate_limit_error","message":"failure"}}`} {
		oldResponse, oldErr := legacy.DecodeResponse(t.Context(), []byte(wire), options)
		newResponse, newErr := draft.DecodeResponse(t.Context(), []byte(wire), options)
		if oldErr == nil {
			if newErr != nil {
				t.Fatal("known error category unexpectedly read HTTP status", newErr)
			}
			oldResponse.Native, newResponse.Native = nil, nil
			_, oldErr = legacy.EncodeResponse(t.Context(), oldResponse, options)
			_, newErr = draft.EncodeResponse(t.Context(), newResponse, options)
		}
		var before, after *p.ConversionError
		if !errors.As(oldErr, &before) || !errors.As(newErr, &after) || len(before.Issues) != 1 || len(after.Issues) != 1 {
			t.Fatal("invalid HTTP context acceptance changed", oldErr, newErr)
		}
		oldIssue, newIssue := before.Issues[0], after.Issues[0]
		if oldIssue.Code != newIssue.Code || oldIssue.Direction != newIssue.Direction || oldIssue.Stage != newIssue.Stage || oldIssue.Path != newIssue.Path {
			t.Fatal("HTTP context error changed its boundary", oldIssue, newIssue)
		}
		if oldIssue.Reason != newIssue.Reason {
			t.Log("NOT CLOSED: legacy decoder text and static invalid-context explanation differ", oldIssue.Reason, newIssue.Reason)
		}
	}
}

func TestResponsesPackageFailureRejections(t *testing.T) {
	checkOpenAIPackageFailureRejections(t, Responses, "openai-responses")
}

func TestChatPackageFailureRejections(t *testing.T) {
	checkOpenAIPackageFailureRejections(t, Chat, "openai-chat")
}

func TestChatPackageFailureValidationOrder(t *testing.T) {
	legacy, draft := shippedProjectionProtocol(t, Chat), compileBuiltinPackage(t, "openai-chat")
	for _, fixture := range []struct{ wire, path string }{
		{`{"error":{"type":"api_error","param":false},"choices":[],"usage":{"prompt_tokens":null}}`, "/error/param"},
		{`{"error":{"type":"api_error","param":false},"usage":{"prompt_tokens":null}}`, "/"},
	} {
		_, oldErr := legacy.DecodeResponse(t.Context(), []byte(fixture.wire), p.EvaluationContext{})
		_, newErr := draft.DecodeResponse(t.Context(), []byte(fixture.wire), p.EvaluationContext{})
		var before, after *p.ConversionError
		if !errors.As(oldErr, &before) || !errors.As(newErr, &after) || len(before.Issues) != 1 || len(after.Issues) != 1 {
			t.Fatal("multi-fault response was accepted", oldErr, newErr)
		}
		oldIssue, newIssue := before.Issues[0], after.Issues[0]
		if oldIssue.Path != fixture.path || oldIssue.Code != newIssue.Code || oldIssue.Direction != newIssue.Direction || oldIssue.Stage != newIssue.Stage || oldIssue.Path != newIssue.Path || oldIssue.Reason != newIssue.Reason {
			t.Fatal("error and usage checks changed order", oldIssue, newIssue)
		}
	}
}

func checkOpenAIPackageFailureRejections(t *testing.T, codec, packageName string) {
	t.Helper()
	legacy, draft := shippedProjectionProtocol(t, codec), compileBuiltinPackage(t, packageName)
	for _, failure := range []string{
		`{"type":"api_error","code":42}`, `{"type":"api_error","param":false}`,
		`{"type":null,"code":42}`, `{"type":"api_error","code":42,"param":false}`,
	} {
		t.Run("decode/"+failure, func(t *testing.T) {
			raw := []byte(`{"error":` + failure + `}`)
			_, oldErr := legacy.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			_, newErr := draft.DecodeResponse(t.Context(), raw, p.EvaluationContext{})
			checkPackageFailureRejection(t, oldErr, newErr)
		})
	}
	for _, failure := range []string{
		`"unmapped"`, `{"message":"failure","vendor":true,"category":"unknown"}`,
		`{"message":"failure","wire:gemini":{"z":null,"a":{"n":9007199254740993}},"category":"unknown"}`,
		`{"message":"failure","wire:gemini":{"z":null,"a":{"n":9007199254740993}},"providerType":"other"}`,
		`{"message":"failure","wire:gemini":{"z":null,"a":{"n":9007199254740993}}}`,
		`{"message":"failure","wire:openai_responses":{"message":"other"},"details":{"key":true}}`,
		`{"message":"failure","details":{"key":true}}`,
		`{"message":"failure","wire:openai_responses":null}`, `{"message":"failure","wire:openai_responses":[]}`,
	} {
		failure = strings.ReplaceAll(failure, "wire:openai_responses", "wire:"+legacy.Identity().Family)
		t.Run("encode/"+failure, func(t *testing.T) {
			response := &p.Response{SchemaVersion: legacy.CoreVersion(), Content: []p.Node{}, Error: testValue(t, failure)}
			_, oldErr := legacy.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			response.SchemaVersion = draft.CoreVersion()
			_, newErr := draft.EncodeResponse(t.Context(), response, p.EvaluationContext{})
			checkPackageFailureRejection(t, oldErr, newErr)
		})
	}
}

func checkPackageFailureRejection(t *testing.T, oldErr, newErr error) {
	t.Helper()
	var before, after *p.ConversionError
	if !errors.As(oldErr, &before) || !errors.As(newErr, &after) || len(before.Issues) != 1 || len(after.Issues) != 1 {
		t.Fatal("failure rejection was lost", oldErr, newErr)
	}
	oldIssue, newIssue := before.Issues[0], after.Issues[0]
	if oldIssue.Code != newIssue.Code || oldIssue.Direction != newIssue.Direction || oldIssue.Stage != newIssue.Stage || oldIssue.Path != newIssue.Path || oldIssue.Reason != newIssue.Reason {
		t.Fatal("failure classification or first error changed", oldIssue, newIssue)
	}
}
