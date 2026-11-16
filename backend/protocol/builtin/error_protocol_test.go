package builtin

import (
	"fmt"
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestProtocolErrorBodyMatrix(t *testing.T) {
	for _, fixture := range []struct {
		class                                                            ErrorClass
		status                                                           int
		openAIType, openAICode, openAIParam, anthropicType, geminiStatus string
	}{
		{ErrorClassInvalidRequest, 400, "invalid_request_error", "", "", "invalid_request_error", "INVALID_ARGUMENT"},
		{ErrorClassAuthentication, 401, "invalid_request_error", "invalid_api_key", "", "authentication_error", "UNAUTHENTICATED"},
		{ErrorClassPermission, 403, "invalid_request_error", "", "", "permission_error", "PERMISSION_DENIED"},
		{ErrorClassModelNotFound, 404, "invalid_request_error", "model_not_found", "model", "not_found_error", "NOT_FOUND"},
		{ErrorClassRateLimit, 429, "rate_limit_error", "", "", "rate_limit_error", "RESOURCE_EXHAUSTED"},
		{ErrorClassOverloaded, 503, "service_unavailable_error", "server_is_overloaded", "", "overloaded_error", "UNAVAILABLE"},
		{ErrorClassUpstream, 502, "api_error", "", "", "api_error", "UNAVAILABLE"},
		{ErrorClassServer, 500, "api_error", "server_error", "", "api_error", "INTERNAL"},
	} {
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(string(fixture.class)+"/"+target, func(t *testing.T) {
				if (&GatewayError{Class: fixture.class}).EffectiveStatus() != fixture.status {
					t.Fatal("host failure status changed")
				}
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Error: testValue(t, fmt.Sprintf(`{"category":%q,"message":"boom"}`, fixture.class))}
				options := fixtureOptions(p.EvaluationContext{Values: p.Object{"httpStatus": testValue(t, fmt.Sprint(fixture.status))}})
				wire, err := testCompiled(t, target).EncodeResponse(t.Context(), response, options)
				if err != nil {
					t.Fatal(err)
				}
				errorBody := p.Object{"message": p.StringValue("boom")}
				expected := p.Object{}
				switch target {
				case Chat, Responses:
					errorBody["type"] = p.StringValue(fixture.openAIType)
					for key, value := range map[string]string{"code": fixture.openAICode, "param": fixture.openAIParam} {
						errorBody[key] = testValue(t, "null")
						if value != "" {
							errorBody[key] = p.StringValue(value)
						}
					}
				case Anthropic:
					expected["type"] = p.StringValue("error")
					errorBody["type"] = p.StringValue(fixture.anthropicType)
				case Gemini:
					errorBody["code"] = testValue(t, fmt.Sprint(fixture.status))
					errorBody["status"] = p.StringValue(fixture.geminiStatus)
				}
				expected["error"], err = p.EncodeValue(errorBody)
				if err != nil {
					t.Fatal(err)
				}
				value, err := p.EncodeValue(expected)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, wire, value.JSONString())
			})
		}
	}
}

func TestProtocolErrorBodyUpstreamOverride(t *testing.T) {
	for _, status := range []int{429, 524} {
		failure := GatewayError{Class: ErrorClassRateLimit, Code: "slow_down", Status: status, Message: "quota"}
		if failure.EffectiveStatus() != status {
			t.Fatal("observed status was replaced")
		}
		response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Error: testValue(t, `{"category":"rate_limit","code":"slow_down","message":"quota"}`)}
		wire, err := testCompiled(t, Chat).EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, `{"error":{"message":"quota","type":"rate_limit_error","param":null,"code":"slow_down"}}`)
	}
}

func TestErrorClassOrDefault(t *testing.T) {
	if ErrorClass("").OrDefault() != ErrorClassServer || (&GatewayError{Message: "x"}).EffectiveStatus() != 500 {
		t.Fatal("unclassified host error did not retain server/500")
	}
}

func TestDeclaredFailureCategories(t *testing.T) {
	for _, fixture := range []struct {
		name, wire, category string
		status               int
	}{
		{"anthropic", `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`, "overloaded", 529},
		{"openai-chat", `{"error":{"type":"rate_limit_error","message":"busy"}}`, "rate_limit", 429},
		{"openai-responses", `{"error":{"type":"service_unavailable_error","message":"busy"}}`, "overloaded", 503},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.name)
			response, err := compiled.DecodeHTTPFailure(t.Context(), fixture.status, []byte(fixture.wire), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			fields, err := response.Error.ReadObject()
			if err != nil || !fields["category"].IsString(fixture.category) {
				t.Fatal("declared failure lost category", response.Error, err)
			}
		})
	}
}
func TestGatewayFailureStatus(t *testing.T) {
	failure := GatewayError{Class: ErrorClassRateLimit, Status: 524}
	if failure.EffectiveStatus() != 524 {
		t.Fatal("observed status replaced")
	}
	if (GatewayError{}).Class.OrDefault() != ErrorClassServer {
		t.Fatal("unknown host error lost server category")
	}
}
