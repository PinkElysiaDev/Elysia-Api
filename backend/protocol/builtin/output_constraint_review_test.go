package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// This file is an unexecuted review supplement for protocol/builtin.
func TestOutputConstraintGeminiSchemaSelectedScope(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	for _, format := range []string{
		`"responseMimeType":"application/json","responseJsonSchema":` + outputConstraintSchema,
		`"responseMimeType":"application/json","responseSchema":{"type":"OBJECT","properties":{"answer":{"type":"STRING"}}}`,
	} {
		body := `{"contents":[],"generationConfig":{` + format + `}}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal("valid schema/MIME pair read from the wrong selected input", err)
		}
		request.Native = nil
		clearEvidenceProvenance(request.Content)
		wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
	}
	for _, format := range []string{
		`"responseMimeType":"text/plain","responseJsonSchema":` + outputConstraintSchema,
		`"responseJsonSchema":` + outputConstraintSchema,
		`"responseMimeType":"application/json","responseJsonSchema":` + outputConstraintSchema + `,"responseSchema":{"type":"OBJECT"}`,
	} {
		body := `{"contents":[],"generationConfig":{` + format + `}}`
		_, err := compiled.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		requireDraftRejection(t, err, "/generationConfig")
	}
}

func TestOutputConstraintChatNullableSchemaOwner(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, strict := range []string{"", `,"strict":null`, `,"strict":false`, `,"strict":true`} {
		body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":null` + strict + `}}}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal("Spec-admitted nullable schema was dereferenced before its Mapping shape gate", err)
		}
		if !request.Parameters["outputConstraint"].IsZero() {
			t.Fatal("null source schema acquired a fabricated neutral constraint")
		}
		request.Native = nil
		clearEvidenceProvenance(request.Content)
		wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, body)
		target := testCompiled(t, Responses)
		projected, err := prepareOutputConstraint(t, compiled, target, request, "compatible", false, nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
		}
		requireContextIssue(t, err, p.ConversionRejected, "/parameters")
	}
}
