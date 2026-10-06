package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestBuiltinHTTPFailureMatrix(t *testing.T) {
	fixtures := map[string]string{
		Chat:      `{"error":{"type":"rate_limit_error","message":"quota","param":null,"code":null}}`,
		Responses: `{"error":{"type":"rate_limit_error","message":"quota"}}`,
		Anthropic: `{"type":"error","error":{"type":"rate_limit_error","message":"quota"}}`,
		Gemini:    `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`,
	}
	options := p.EvaluationContext{Values: p.Object{"httpStatus": testValue(t, `429`)}}
	for source, fixture := range fixtures {
		for target := range fixtures {
			t.Run(source+"/"+target, func(t *testing.T) {
				decoder, encoder := testCompiled(t, source), testCompiled(t, target)
				failure, err := decoder.DecodeHTTPFailure(t.Context(), 429, []byte(fixture), options)
				if err != nil {
					t.Fatal(err)
				}
				body, err := encoder.EncodeResponse(t.Context(), failure, options)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := encoder.DecodeHTTPFailure(t.Context(), 429, body, options)
				if err != nil {
					t.Fatal(err)
				}
				var before, after any
				if err := failure.Error.Decode(&before); err != nil {
					t.Fatal(err)
				}
				if err := decoded.Error.Decode(&after); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("failure semantics changed: %s -> %s", failure.Error.Bytes(), decoded.Error.Bytes())
				}
				if !strings.Contains(string(body), "quota") || p.CheckGenerationOutcome(decoded) == nil {
					t.Fatal("error became success")
				}
			})
		}
	}
}

func TestBuiltinFailureNativeExtensionsStayScoped(t *testing.T) {
	decoder := testCompiled(t, Responses)
	raw := `{"error":{"message":"limited","type":"vendor_budget","code":"vendor_code","trace":{"n":9007199254740993,"enabled":false,"detail":null}},"vendor":[]}`
	response, err := decoder.DecodeHTTPFailure(t.Context(), 403, []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := decoder.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	json.Unmarshal([]byte(raw), &before)
	json.Unmarshal(encoded, &after)
	if !reflect.DeepEqual(before, after) || !strings.Contains(string(encoded), "9007199254740993") {
		t.Fatalf("native error changed: %s", encoded)
	}
	_, err = testCompiled(t, Anthropic).EncodeResponse(t.Context(), response, p.EvaluationContext{})
	var conversion *p.ConversionError
	if !errors.As(err, &conversion) || conversion.Issues[0].Code != p.UnsupportedCapability {
		t.Fatalf("unknown error extensions crossed provider: %v", err)
	}
}

func TestStreamFailureUsesSharedClassification(t *testing.T) {
	decoder, encoder := testCompiled(t, Anthropic), testCompiled(t, Chat)
	frame, err := decoder.DecodeFrame(t.Context(), testValue(t, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	frames, err := encoder.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), `"type":"service_unavailable_error"`) || !strings.Contains(string(frames[0].Bytes()), `"code":"server_is_overloaded"`) {
		t.Fatalf("wrong failure class: %v", frames)
	}
}
