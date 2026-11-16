package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const anthropicOutputBoundaryPrefix = `{"model":"m","max_tokens":8192,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]`

func compileAnthropicGenerationBoundary(t *testing.T, compiled *p.Compiled) *p.Compiled {
	t.Helper()
	document, _ := compiled.Package()
	entry := document.Mapping.Directions[p.DecodeRequest]
	entry.Document = "generation-request"
	document.Mapping.Directions[p.DecodeRequest] = entry
	generation, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestAnthropicOutputConfigNullableBoundary(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	generation := compileAnthropicGenerationBoundary(t, compiled)
	for _, fixture := range []struct {
		name, suffix  string
		hasConstraint bool
	}{
		{"absent", "", false},
		{"null", `,"output_config":null`, false},
		{"null-with-thinking", `,"thinking":{"type":"disabled"},"output_config":null`, false},
		{"empty", `,"output_config":{}`, false},
		{"effort", `,"output_config":{"effort":"high"}`, false},
		{"unknown-field", `,"output_config":{"future":{"fraction":9007199254740993.25}}`, false},
		{"known-format", `,"output_config":{"format":{"type":"json_schema","schema":` + outputConstraintSchema + `}}`, true},
		{"owned-format-member", `,"output_config":{"format":{"type":"json_schema","schema":` + outputConstraintSchema + `,"future":false}}`, false},
		{"owned-schema-keyword", `,"output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"answer":{"type":"string","enum":["Exact"]}},"required":["answer"],"additionalProperties":false}}}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw := anthropicOutputBoundaryPrefix + fixture.suffix + `}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(raw), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			if request.Parameters["outputConstraint"].IsZero() == fixture.hasConstraint {
				t.Fatal("output config presence or ownership changed", request.Parameters)
			}
			if _, err := generation.DecodeRequest(t.Context(), []byte(raw), newDraftAuditOptions()); err != nil {
				t.Fatal("generation document disagrees with source null/format contract", err)
			}
			for _, hasNative := range []bool{true, false} {
				candidate := request.Clone()
				if !hasNative {
					candidate.Native = nil
				}
				wire := encodeDraftRequest(t, compiled, candidate)
				sameJSON(t, wire.Bytes(), raw)
				if hasNative && wire.JSONString() != raw {
					t.Fatal("unchanged source lost its exact original bytes")
				}
			}
		})
	}
}

func TestAnthropicOutputFormatInvalidBoundary(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	generation := compileAnthropicGenerationBoundary(t, compiled)
	for _, fixture := range []struct{ name, config, path, reason string }{
		{"scalar-config", `false`, "/output_config", "thinking controls must be an object or null"},
		{"array-config", `[]`, "/output_config", "thinking controls must be an object or null"},
		{"null-format", `{"format":null}`, "/output_config/format", "output format requires a recognized typed object"},
		{"false-format", `{"format":false}`, "/output_config/format", "output format requires a recognized typed object"},
		{"array-format", `{"format":[]}`, "/output_config/format", "output format requires a recognized typed object"},
		{"missing-type", `{"format":{}}`, "/output_config/format", "output format requires a recognized typed object"},
		{"null-type", `{"format":{"type":null}}`, "/output_config/format", "output format requires a recognized typed object"},
		{"unknown-type", `{"format":{"type":"future","schema":{}}}`, "/output_config/format", "output format requires a recognized typed object"},
		{"missing-schema", `{"format":{"type":"json_schema"}}`, "/output_config/format", "schema format requires its declared members"},
		{"null-schema", `{"format":{"type":"json_schema","schema":null}}`, "/output_config/format", "schema format requires its declared members"},
		{"array-schema", `{"format":{"type":"json_schema","schema":[]}}`, "/output_config/format", "schema format requires its declared members"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw := anthropicOutputBoundaryPrefix + `,"output_config":` + fixture.config + `}`
			_, decodeError := compiled.DecodeRequest(t.Context(), []byte(raw), newDraftAuditOptions())
			_, generationError := generation.DecodeRequest(t.Context(), []byte(raw), newDraftAuditOptions())
			for index, err := range []error{decodeError, generationError} {
				var failure *p.ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) == 0 {
					t.Fatal("invalid present format escaped the Spec", index, err)
				}
				issue := failure.Issues[0]
				if issue.Code != p.InvalidInput || issue.Path != fixture.path || issue.Reason != fixture.reason || strings.Contains(fmt.Sprint(err), "JSON pointer traverses a scalar") {
					t.Fatal("invalid format changed its declared first failure", index, issue, err)
				}
			}
		})
	}
}
