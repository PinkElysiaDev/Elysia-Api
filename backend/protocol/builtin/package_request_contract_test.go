package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageRequestExternalContracts(t *testing.T) {
	for _, fixture := range []struct {
		name, prefix string
		fields       []finalWireCase
	}{
		{"anthropic", `{"model":"m","max_tokens":32,"messages":[]`, []finalWireCase{
			{"metadata-array", `"metadata":[]`, "/metadata"},
			{"user-number", `"metadata":{"user_id":7}`, "/metadata/user_id"},
			{"unknown-native-metadata", `"metadata":{"future":null,"user_id":null}`, acceptedFinalWire},
		}},
		{"openai-chat", `{"model":"m","messages":[]`, []finalWireCase{
			{"metadata-value", `"metadata":{"trace":7}`, "/metadata"},
			{"user-object", `"user":{}`, "/user"},
			{"empty-and-null", `"metadata":{},"user":null`, acceptedFinalWire},
		}},
		{"openai-responses", `{"model":"m","input":[]`, []finalWireCase{
			{"metadata-null-member", `"metadata":{"trace":null}`, "/metadata"},
			{"user-boolean", `"user":false`, "/user"},
			{"truncation-future", `"truncation":"future"`, "/truncation"},
			{"reference-empty", `"previous_response_id":""`, "/previous_response_id"},
			{"null-context", `"truncation":null,"previous_response_id":null,"metadata":{"trace":"local"}`, acceptedFinalWire},
		}},
		{"google-generate-content", `{"contents":[]`, []finalWireCase{
			{"safety-object", `"safetySettings":{}`, "/safetySettings"},
			{"missing-threshold", `"safetySettings":[{"category":"future"}]`, "/safetySettings/0/threshold"},
			{"empty-category", `"safetySettings":[{"category":"","threshold":"OFF"}]`, "/safetySettings/0/category"},
			{"duplicate-category", `"safetySettings":[{"category":"future","threshold":"OFF"},{"category":"futur\u0065","threshold":"OFF"}]`, "/safetySettings"},
			{"unknown-native-safety", `"safetySettings":[{"category":"future","threshold":"future","extra":null}]`, acceptedFinalWire},
			{"null-safety", `"safetySettings":null`, acceptedFinalWire},
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, fixture.name)
			for _, field := range fixture.fields {
				t.Run(field.name, func(t *testing.T) {
					wire := fixture.prefix + "," + field.wire + "}"
					request, err := compiled.DecodeRequest(t.Context(), []byte(wire), p.EvaluationContext{})
					if field.path != acceptedFinalWire {
						requireContextIssue(t, err, p.InvalidInput, field.path)
						requireContextIssue(t, compiled.ValidateRequestOutput(t.Context(), testValue(t, wire), p.EvaluationContext{}), p.ConversionRejected, field.path)
						return
					}
					if err != nil {
						t.Fatal("valid source request rejected", err)
					}
					request.Native = nil
					options := p.EvaluationContext{ClientOutput: request.ClientOutput}
					output, err := compiled.EncodeRequestValue(t.Context(), request, options)
					if err != nil {
						t.Fatal("valid rebuilt request rejected", err)
					}
					if err := compiled.ValidateRequestOutput(t.Context(), output, options); err != nil {
						t.Fatal("valid final request rejected", err)
					}
				})
			}
		})
	}
}

func TestPackageRequestTrackingRejectsMalformedCore(t *testing.T) {
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, name)
			for _, raw := range []string{
				`{"anthropic_metadata":"ignored"}`,
				`{"anthropic_metadata":{"user_id":1}}`,
				`{"user":1}`,
				`{"metadata":{"trace":null}}`,
			} {
				input := testValue(t, `{"schemaVersion":2,"model":"m","content":[],"parameters":`+raw+"}")
				_, issues := compiled.Execute(t.Context(), p.EncodeRequest, input, p.EvaluationContext{})
				if len(issues) != 1 || issues[0].Code != p.InvalidInput && issues[0].Code != p.ConversionRejected {
					t.Fatal("malformed source tracking data was silently omitted", raw, issues)
				}
				if !strings.HasPrefix(issues[0].Path, "/parameters/") && issues[0].Path != "/metadata" && issues[0].Path != "/user" {
					t.Fatal("unrelated guard hid the tracking error", raw, issues)
				}
			}
		})
	}
}
