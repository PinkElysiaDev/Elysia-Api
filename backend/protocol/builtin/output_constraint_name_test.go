package builtin

import (
	"errors"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const outputNameLimit = 64

type outputNameFixture struct {
	id, name string
	isValid  bool
}

func outputNameFixtures() []outputNameFixture {
	return []outputNameFixture{
		{"empty-without-declared-minimum", "", true},
		{"alphabet", "Az09_-", true},
		{"maximum", strings.Repeat("a", outputNameLimit), true},
		{"space", "bad name", false},
		{"non-ascii", "名称", false},
		{"slash", "bad/name", false},
		{"line-break", "name\n", false},
		{"oversized", strings.Repeat("a", outputNameLimit+1), false},
	}
}

func requireOutputNameIssue(t *testing.T, err error, code p.IssueCode, path string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
		return issue.Code == code && issue.Path == path
	}) {
		t.Fatal("name contract lost its exact diagnostic", code, path, err)
	}
}

func TestOutputConstraintNameSourceContract(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		t.Run(codec, func(t *testing.T) {
			compiled := testCompiled(t, codec)
			samples := packageMapping(compiled).Samples
			index := slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })
			if index < 0 {
				t.Fatal("missing output-format sample")
			}
			encodeIndex := slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == "output-constraint-encode" })
			if encodeIndex < 0 {
				t.Fatal("missing output-format reconstruction sample")
			}
			format := outputFormatPath(codec)
			path := append(slices.Clone(format), "name")
			for _, fixture := range outputNameFixtures() {
				for _, strict := range []string{"missing", "null", "false", "true"} {
					t.Run(fixture.id+"/"+strict, func(t *testing.T) {
						body := editOutputWire(t, samples[index].Input, path, p.StringValue(fixture.name))
						var enforcement p.Value
						if strict != "missing" {
							enforcement = testValue(t, strict)
						}
						body = editOutputWire(t, body, append(slices.Clone(format), "strict"), enforcement)
						request, err := compiled.DecodeRequest(t.Context(), body.Bytes(), fixtureOptions(p.EvaluationContext{}))
						if !fixture.isValid {
							requireOutputNameIssue(t, err, p.InvalidInput, "/"+strings.Join(path, "/"))
							return
						}
						if err != nil {
							t.Fatal("documented source name was refused", err)
						}
						request.Native = nil
						clearEvidenceProvenance(request.Content)
						wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
						if err != nil {
							t.Fatal(err)
						}
						expected := editOutputWire(t, samples[encodeIndex].Expected, format, readOutputWire(t, body, format...))
						sameJSON(t, wire, expected.JSONString())
					})
				}
			}
		})
	}
}

func TestOutputConstraintNameNeutralTargetContract(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		t.Run(codec, func(t *testing.T) {
			for _, fixture := range outputNameFixtures() {
				for _, enforcement := range []string{"required", "preferred"} {
					t.Run(fixture.id+"/"+enforcement, func(t *testing.T) {
						compiled, request := decodeOutputConstraintFixture(t, codec)
						constraint := editOutputWire(t, request.Parameters["outputConstraint"], []string{"name"}, p.StringValue(fixture.name))
						request.Parameters["outputConstraint"] = editOutputWire(t, constraint, []string{"enforcement"}, p.StringValue(enforcement))
						input, err := p.EncodeValue(request)
						if err != nil {
							t.Fatal(err)
						}
						for _, encode := range []func() (p.Value, error){
							func() (p.Value, error) {
								return compiled.EncodeRequestValue(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
							},
							func() (p.Value, error) {
								wire, issues := compiled.Execute(t.Context(), p.EncodeRequest, input, fixtureOptions(p.EvaluationContext{}))
								return wire, p.IssuesError(issues)
							},
						} {
							wire, err := encode()
							if !fixture.isValid {
								requireOutputNameIssue(t, err, p.UnsupportedCapability, outputConstraintCorePath+"/name")
								continue
							}
							if err != nil {
								t.Fatal("documented neutral name was refused", err)
							}
							name := readOutputWire(t, wire, append(outputFormatPath(codec), "name")...)
							if !name.IsString(fixture.name) {
								t.Fatal("target sanitized or generated a format name", name)
							}
						}
					})
				}
			}
		})
	}
}
