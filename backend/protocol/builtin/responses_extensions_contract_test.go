package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesExtensionTransformationReasons(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, reason := range []string{"organization_mismatch", "end_user_mismatch", "model_mismatch", "prefix_mismatch", "turn_boundary"} {
		body := buildResponseMetadataBody(fmt.Sprintf(`"input_transformations":[{"id":"source-input","type":"reasoning","action":"removed","reason":%q}]`, reason))
		response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		canonical := fmt.Sprintf(`[{"sourceItemId":"source-input","kind":"reasoning","action":"remove","reason":%q}]`, strings.ReplaceAll(reason, "_", "-"))
		requireResponseMetadata(t, response.Metadata, "input_transformations", canonical, "input-transformations", "")
		response.Native = nil
		clearMediaScopeNative(response.Content)
		wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
	}
	for _, missing := range []string{`"id":"source-2",`, `"type":"reasoning",`, `"action":"removed",`, `,"reason":"model_mismatch"`} {
		body := buildResponseMetadataBody(`"input_transformations":` + strings.Replace(responseTransformation, missing, "", 1))
		_, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		requireDraftRejection(t, err, "/input_transformations")
	}
}

func TestResponsesExtensionAbsenceAndNullRetention(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	response, err := from.DecodeResponse(t.Context(), []byte(responsesMetadataBase), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range response.Metadata {
		if field.Meaning == "image-context-compression" || field.Meaning == "input-transformations" {
			t.Fatal("absent observations were invented", field)
		}
	}
	fields := strings.Replace(responseZeroRetentionUsage, `{"ttl_30m_tokens":0,"ttl_12h_tokens":0}`, `null`, 1)
	body := buildResponseMetadataBody(fields)
	response, err = from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cache_creation.30m_tokens", "cache_creation.12h_tokens"} {
		if _, hasCounter := response.Usage.Details[name]; hasCounter {
			t.Fatal("unreported retention was defaulted to zero", response.Usage)
		}
	}
	response.Native = nil
	clearMediaScopeNative(response.Content)
	wire, err := from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, body)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionResponse, "compatible", "usage-projection")
		projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), nil)
		if err == nil {
			_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		requireDraftRejection(t, err, "")
	}
	response.Usage.Details = map[string]p.Counter{
		"cache_creation.30m_tokens": {Count: 0, Origin: p.ObservedCount},
		"cache_creation.12h_tokens": {Count: 0, Origin: p.ObservedCount},
	}
	_, err = from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	requireDraftRejection(t, err, "")
}

func TestResponsesExtensionTypedCoreBoundary(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Chat)
	for _, fixture := range []struct{ field, wire, canonical string }{
		{"image", `{"context_compression":"disabled"}`, `"disabled"`},
		{"input_transformations", `[]`, `[{"sourceItemId":"source","kind":"reasoning","action":"removed","reason":"model-mismatch"}]`},
	} {
		response, err := from.DecodeResponse(t.Context(), []byte(buildResponseMetadataBody(fmt.Sprintf("%q:%s", fixture.field, fixture.wire))), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		response.Native = nil
		setResponseObservationValue(response.Metadata, fixture.field, testValue(t, fixture.canonical))
		_, err = from.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		requireDraftRejection(t, err, "/"+fixture.field)
		conversion := resolveResponseExtensionConversion(t, from, to, p.ConversionResponse, "compatible", "metadata")
		_, err = conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON).WithOperation(fixtureOptions(p.EvaluationContext{})), nil)
		requireDraftRejection(t, err, "/"+fixture.field)
	}
}
