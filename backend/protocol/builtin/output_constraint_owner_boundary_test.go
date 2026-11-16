package builtin

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const outputOwnerConflictReason = "output constraint has both semantic and source-owned representations"

type outputOwnerFixture struct {
	name, owner string
	isDuplicate bool
}

func buildOutputOwnerFixtures(container string, fields []string) []outputOwnerFixture {
	fixtures := []outputOwnerFixture{{name: "absent"}, {name: "empty", owner: `{}`}}
	for _, value := range []string{`null`, `false`, `0`, `"scalar"`, `[]`} {
		fixtures = append(fixtures, outputOwnerFixture{name: "owner-" + value, owner: value})
		if container != "" {
			fixtures = append(fixtures, outputOwnerFixture{name: "container-" + value, owner: fmt.Sprintf(`{%q:%s}`, container, value)})
		}
	}
	if container != "" {
		fixtures = append(fixtures, outputOwnerFixture{name: "empty-container", owner: fmt.Sprintf(`{%q:{}}`, container)})
	}
	for _, field := range fields {
		for _, value := range []string{`null`, `{}`} {
			owner := fmt.Sprintf(`{%q:%s}`, field, value)
			if container != "" {
				owner = fmt.Sprintf(`{%q:%s}`, container, owner)
			}
			fixtures = append(fixtures, outputOwnerFixture{name: "duplicate-" + field + "-" + value, owner: owner, isDuplicate: true})
		}
	}
	return fixtures
}

// Disable only the newly introduced conflict predicate in a test-local package.
// Its unchanged program remains the oracle for malformed-owner diagnostics.
func compileOutputOwnerReference(t *testing.T, compiled *p.Compiled) *p.Compiled {
	t.Helper()
	document, _ := compiled.Package()
	entry := document.Mapping.Directions[p.EncodeRequest]
	index := slices.IndexFunc(entry.Effects, func(effect p.ProtocolSemanticEffect) bool { return effect.ID == "output-constraint-owner" })
	if index < 0 {
		t.Fatal("missing output owner conflict effect")
	}
	entry.Effects[index].When = &p.Expression{Op: "literal", Value: testValue(t, `false`)}
	document.Mapping.Directions[p.EncodeRequest] = entry
	reference, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return reference
}

func decodeOutputOwnerRequest(t *testing.T, compiled *p.Compiled) *p.Request {
	t.Helper()
	samples := packageMapping(compiled).Samples
	index := slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })
	if index < 0 {
		t.Fatal("missing output constraint input fixture")
	}
	sample := samples[index]
	request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), fixtureOptions(p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}))
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	clearEvidenceProvenance(request.Content)
	return request
}

func encodeOutputOwnerBoundary(t *testing.T, compiled *p.Compiled, original *p.Request, owner string, fixture outputOwnerFixture, hasConstraint bool) (p.Value, error) {
	t.Helper()
	request := original.Clone()
	if !hasConstraint {
		delete(request.Parameters, "outputConstraint")
	}
	delete(request.Parameters, owner)
	if fixture.owner != "" {
		request.Parameters[owner] = testValue(t, fixture.owner)
	}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	wire, encodingError := compiled.EncodeRequestValue(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
	after, err := p.EncodeValue(request)
	if err != nil || before != after {
		t.Fatal("owner validation changed the caller's request", err)
	}
	return wire, encodingError
}

func TestOutputConstraintOwnerGuardBoundaries(t *testing.T) {
	for _, target := range []struct {
		codec, owner, container string
		fields                  []string
	}{
		{Chat, "wire:openai_chat", "", []string{"response_format"}},
		{Responses, "wire:openai_responses", "text", []string{"format"}},
		{Anthropic, "wire:claude", "output_config", []string{"format"}},
		{Gemini, "wire:gemini", "generationConfig", []string{"responseMimeType", "responseSchema", "responseJsonSchema"}},
	} {
		t.Run(target.codec, func(t *testing.T) {
			compiled := testCompiled(t, target.codec)
			reference := compileOutputOwnerReference(t, compiled)
			request, referenceRequest := decodeOutputOwnerRequest(t, compiled), decodeOutputOwnerRequest(t, reference)
			for _, hasConstraint := range []bool{false, true} {
				for _, fixture := range buildOutputOwnerFixtures(target.container, target.fields) {
					t.Run(fmt.Sprintf("constraint-%t/%s", hasConstraint, fixture.name), func(t *testing.T) {
						wire, err := encodeOutputOwnerBoundary(t, compiled, request, target.owner, fixture, hasConstraint)
						if hasConstraint && fixture.isDuplicate {
							var failure *p.ConversionError
							if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != outputConstraintCorePath || failure.Issues[0].Reason != outputOwnerConflictReason || failure.Issues[0].RuleID != "output-constraint-owner" {
								t.Fatal("duplicate representation escaped its declared rejection", err)
							}
							return
						}
						want, expectedError := encodeOutputOwnerBoundary(t, reference, referenceRequest, target.owner, fixture, hasConstraint)
						if fmt.Sprint(err) != fmt.Sprint(expectedError) {
							t.Fatal("conflict guard changed the existing owner boundary", err, expectedError)
						}
						if err == nil {
							sameJSON(t, wire.Bytes(), want.JSONString())
							return
						}
						var actualFailure, expectedFailure *p.ConversionError
						if !errors.As(err, &actualFailure) || !errors.As(expectedError, &expectedFailure) {
							t.Fatal("owner rejection lost structured diagnostics", err, expectedError)
						}
						if !reflect.DeepEqual(actualFailure.Issues, expectedFailure.Issues) {
							t.Fatal("owner rejection changed its full issue record", actualFailure.Issues, expectedFailure.Issues)
						}
					})
				}
			}
		})
	}
}
