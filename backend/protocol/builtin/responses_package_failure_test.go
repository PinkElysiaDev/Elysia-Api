package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type failureEnvelopeCase struct {
	name, wire, status, rebuiltError string
	shouldPromoteStatus              bool
}

func requireFailureRejection(t *testing.T, err error) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) == 0 {
		t.Fatal("invalid failure lost its structured rejection", err)
	}
}

func checkPackageFailureEnvelope(t *testing.T, compiled *p.Compiled, sample failureEnvelopeCase) {
	t.Helper()
	options := fixtureOptions(p.EvaluationContext{})
	if sample.status != "" {
		options.Values = p.Object{"httpStatus": testValue(t, sample.status)}
	}
	response, err := compiled.DecodeResponse(t.Context(), []byte(sample.wire), options)
	if err != nil {
		t.Fatal("declared failure stopped decoding", err)
	}
	var failure *p.GenerationFailure
	if !errors.As(p.CheckGenerationOutcome(response), &failure) {
		t.Fatal("provider failure became a successful response")
	}
	fields, _ := testValue(t, sample.wire).ReadObject()
	expectedMessage := fields["error"]
	if expectedMessage.IsObject() {
		errorFields, _ := expectedMessage.ReadObject()
		expectedMessage = errorFields["message"]
	}
	semantic, _ := response.Error.ReadObject()
	sameJSON(t, semantic["message"].Bytes(), expectedMessage.JSONString())
	encoded, err := compiled.EncodeResponse(t.Context(), response, options)
	if err != nil {
		t.Fatal("native failure replay", err)
	}
	sameJSON(t, encoded, sample.wire)
	response.Native = nil
	encoded, err = compiled.EncodeResponse(t.Context(), response, options)
	if err != nil {
		t.Fatal("failure reconstruction", err)
	}
	rebuilt, err := compiled.DecodeResponse(t.Context(), encoded, options)
	if err != nil || p.CheckGenerationOutcome(rebuilt) == nil {
		t.Fatal("reconstructed failure became successful", string(encoded), err)
	}
	expectedError := response.Error
	if sample.rebuiltError != "" {
		expectedError = testValue(t, sample.rebuiltError)
	}
	sameJSON(t, rebuilt.Error.Bytes(), expectedError.JSONString())
	if sample.shouldPromoteStatus {
		owner, err := response.Attributes["wire:openai_chat"].ReadObject()
		if err != nil || rebuilt.Status != owner["status"] || !rebuilt.Status.IsString("failed") {
			t.Fatal("error-envelope reconstruction lost the original status", rebuilt.Status, owner, err)
		}
		delete(owner, "status")
		if len(owner) == 0 {
			delete(response.Attributes, "wire:openai_chat")
			if len(response.Attributes) == 0 {
				response.Attributes = nil
			}
		} else {
			response.Attributes["wire:openai_chat"], err = p.EncodeValue(owner)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	beforeAttributes, _ := p.EncodeValue(response.Attributes)
	afterAttributes, _ := p.EncodeValue(rebuilt.Attributes)
	sameJSON(t, afterAttributes.Bytes(), beforeAttributes.JSONString())
}

func TestResponsesPackageFailureEnvelopes(t *testing.T) {
	checkOpenAIPackageFailureEnvelopes(t, Responses)
}

func TestChatPackageFailureEnvelopes(t *testing.T) {
	checkOpenAIPackageFailureEnvelopes(t, Chat)
}

func checkOpenAIPackageFailureEnvelopes(t *testing.T, codec string) {
	t.Helper()
	compiled := testCompiled(t, codec)
	for _, fixture := range []failureEnvelopeCase{
		{name: "rate-limit", wire: `{"error":{"type":"rate_limit_error","message":"quota","code":null,"param":null}}`, status: "429"},
		{name: "authentication", wire: `{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"credentials"}}`, status: "401"},
		{name: "missing-model", wire: `{"error":{"type":"invalid_request_error","code":"model_not_found","param":"model","message":"missing"}}`, status: "404"},
		{name: "overloaded", wire: `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"busy"}}`, status: "503"},
		{name: "server-alias", wire: `{"error":{"type":"server_error","message":"failed"}}`, status: "500"},
		{name: "upstream-alias", wire: `{"error":{"type":"upstream_error","code":"upstream_error","message":"failed"}}`, status: "502"},
		{name: "wrapper", wire: `{"error":{"type":"new_api_error","code":"vendor_limit","message":"limited"}}`, status: "403"},
		{name: "redundant-code", wire: `{"error":{"type":"rate_limit_error","code":"rate_limit_error","message":"quota"}}`, status: "429"},
		{name: "string", wire: `{"error":"failed"}`, status: "500", rebuiltError: `{"category":"upstream","message":"failed"}`},
		{name: "escaped-string", wire: `{"error":"f\u0061iled<&\/next"}`, status: "500", rebuiltError: `{"category":"upstream","message":"failed<&/next"}`},
		{name: "unknown-fields", wire: `{"error":{"message":"limited","type":"vendor_budget","code":"vendor_code","trace":{"n":9007199254740993,"enabled":false,"detail":null}},"vendor":[]}`, status: "403"},
		{name: "content-and-error", wire: `{"id":"r","model":"m","status":"failed","output":[],"error":{"type":"api_error","message":"failed"},"usage":{"input_tokens":2,"output_tokens":0}}`, status: "500"},
		{name: "without-status", wire: `{"error":{"type":"server_error","message":"failed"}}`, status: ""},
		{name: "wrapper-without-status", wire: `{"error":{"type":"new_api_error","message":"failed"}}`, status: ""},
		{name: "unclassified-without-status", wire: `{"error":{"message":"failed"}}`, status: "", rebuiltError: `{"category":"upstream","message":"failed"}`},
		{name: "unclassified-null-status", wire: `{"error":{"message":"failed"}}`, status: "null"},
		{name: "envelope-extensions", wire: `{"error":{"type":"rate_limit_error","message":"quota"},"store":"provider-state","tool_usage":42}`, status: "429"},
	} {
		if codec == Chat && fixture.name == "content-and-error" {
			fixture.shouldPromoteStatus = true
			fixture.wire = `{"id":"r","model":"m","status":"failed","choices":[],"error":{"type":"api_error","message":"failed"},"usage":{"prompt_tokens":2,"completion_tokens":0}}`
		}
		t.Run(fixture.name, func(t *testing.T) {
			checkPackageFailureEnvelope(t, compiled, fixture)
		})
	}
}

func TestResponsesPackageFailureRejections(t *testing.T) {
	checkOpenAIPackageFailureRejections(t, Responses)
}

func TestChatPackageFailureRejections(t *testing.T) {
	checkOpenAIPackageFailureRejections(t, Chat)
}

func TestResponsesPackageFailureContextBoundary(t *testing.T) {
	checkOpenAIPackageFailureContext(t, Responses)
}

func TestChatPackageFailureContextBoundary(t *testing.T) {
	checkOpenAIPackageFailureContext(t, Chat)
}

func checkOpenAIPackageFailureContext(t *testing.T, codec string) {
	t.Helper()
	compiled := testCompiled(t, codec)
	options := fixtureOptions(p.EvaluationContext{Values: p.Object{"httpStatus": p.StringValue("invalid")}})
	for _, sample := range []struct {
		wire      string
		isKnown   bool
		direction p.Direction
		issueCode p.IssueCode
	}{
		{`{"error":{"message":"failure"}}`, false, p.DecodeResponse, p.UpstreamContractViolation},
		{`{"error":{"type":"rate_limit_error","message":"failure"}}`, true, p.EncodeResponse, p.InvalidInput},
	} {
		t.Run(sample.wire, func(t *testing.T) {
			response, err := compiled.DecodeResponse(t.Context(), []byte(sample.wire), options)
			if sample.isKnown {
				if err != nil {
					t.Fatal("known error category unexpectedly read HTTP status", err)
				}
				fields, readErr := response.Error.ReadObject()
				if readErr != nil || !fields["category"].IsString("rate_limit") {
					t.Fatal("known category was not retained", response.Error, readErr)
				}
				response.Native = nil
				_, err = compiled.EncodeResponse(t.Context(), response, options)
			}
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal("invalid HTTP context was accepted", err)
			}
			issue := failure.Issues[0]
			if issue.Direction != sample.direction || issue.Code != sample.issueCode || issue.Stage != "runtime" || issue.Path != "/" {
				t.Fatal("invalid HTTP context changed its boundary", issue)
			}
		})
	}
}

func TestChatPackageFailureValidationOrder(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, wire := range []string{
		`{"error":{"type":"api_error","param":false},"choices":[],"usage":{"prompt_tokens":null}}`,
		`{"error":{"type":"api_error","param":false},"usage":{"prompt_tokens":null}}`,
		`{"error":{"type":"api_error","param":false,"message":"failed"}}`,
		`{"choices":[],"usage":{"prompt_tokens":null}}`,
	} {
		t.Run(wire, func(t *testing.T) {
			_, err := compiled.DecodeResponse(t.Context(), []byte(wire), fixtureOptions(p.EvaluationContext{}))
			requireFailureRejection(t, err)
		})
	}
}

func checkOpenAIPackageFailureRejections(t *testing.T, codec string) {
	t.Helper()
	compiled := testCompiled(t, codec)
	for _, failure := range []string{
		`{"type":"api_error","code":42}`, `{"type":"api_error","param":false}`,
		`{"type":null,"code":42}`, `{"type":"api_error","code":42,"param":false}`,
	} {
		t.Run("decode/"+failure, func(t *testing.T) {
			raw := []byte(`{"error":` + failure + `}`)
			_, err := compiled.DecodeResponse(t.Context(), raw, fixtureOptions(p.EvaluationContext{}))
			requireFailureRejection(t, err)
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
		failure = strings.ReplaceAll(failure, "wire:openai_responses", "wire:"+compiled.Identity().Family)
		t.Run("encode/"+failure, func(t *testing.T) {
			response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{}, Error: testValue(t, failure)}
			_, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			requireFailureRejection(t, err)
		})
	}
}
