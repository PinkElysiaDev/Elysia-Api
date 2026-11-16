package builtin

import (
	"fmt"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	outputNumericEnumSchema = `{"type":"object","properties":{"answer":{"type":"number","enum":[9007199254740993,9007199254740992,1.25,1e-3,-0,0.0,1,1.0,10e-1]}},"required":["answer"],"additionalProperties":false}`
	outputIntegerEnumSchema = `{"type":"object","properties":{"answer":{"type":"integer","enum":[9007199254740993,9223372036854775808,1.0,10e-1,1e4001]}},"required":["answer"],"additionalProperties":false}`
	outputFixedEnumSchema   = `{"type":"object","properties":{"answer":{"type":"integer","enum":[9007199254740993]}},"required":["answer"],"additionalProperties":false}`
)

func outputSchemaPath(codec string) []string {
	key := "schema"
	if codec == Gemini {
		key = "responseJsonSchema"
	}
	return append(outputFormatPath(codec), key)
}

func decodeOutputEnumFixture(t *testing.T, codec, schema string) (*p.Compiled, *p.Request) {
	t.Helper()
	compiled := testCompiled(t, codec)
	samples := packageMapping(compiled).Samples
	sample := samples[slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })]
	body := editOutputWire(t, sample.Input, outputSchemaPath(codec), testValue(t, schema))
	request, err := compiled.DecodeRequest(t.Context(), body.Bytes(), fixtureOptions(p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}))
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	clearEvidenceProvenance(request.Content)
	return compiled, request
}

func encodeOutputEnum(t *testing.T, source, target *p.Compiled, request *p.Request) ([]byte, error) {
	t.Helper()
	prepared, err := prepareOutputConstraint(t, source, target, request, "compatible", false, nil)
	if err != nil {
		return nil, err
	}
	return target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
}

func requireOutputEnumLexemes(t *testing.T, schema p.Value, expected string) {
	t.Helper()
	var actualMembers, expectedMembers []p.Value
	if err := readOutputWire(t, schema, "properties", "answer", "enum").Decode(&actualMembers); err != nil {
		t.Fatal(err)
	}
	if err := readOutputWire(t, testValue(t, expected), "properties", "answer", "enum").Decode(&expectedMembers); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actualMembers, expectedMembers) {
		t.Fatal("enum values, spelling, duplicates or order changed", actualMembers, expectedMembers)
	}
}

func TestOutputNumericEnumNativeAndEditedInverse(t *testing.T) {
	testOutputEnumNativeAndEditedInverse(t, []string{Chat, Responses, Anthropic, Gemini}, map[string]string{
		"number": outputNumericEnumSchema, "integer": outputIntegerEnumSchema, "singleton": outputFixedEnumSchema,
	}, outputFixedEnumSchema)
}

func testOutputEnumNativeAndEditedInverse(t *testing.T, codecs []string, schemas map[string]string, replacementSchema string) {
	t.Helper()
	for _, codec := range codecs {
		for name, schema := range schemas {
			t.Run(codec+"/"+name, func(t *testing.T) {
				compiled, request := decodeOutputEnumFixture(t, codec, schema)
				requireOutputEnumLexemes(t, readOutputWire(t, request.Parameters["outputConstraint"], "schema"), schema)
				for _, editedSchema := range []string{schema, replacementSchema} {
					setOutputConstraintSchema(t, request, editedSchema)
					before, err := p.EncodeValue(request)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := encodeOutputEnum(t, compiled, compiled, request)
					if err != nil {
						t.Fatal(err)
					}
					requireOutputEnumLexemes(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...), editedSchema)
					after, err := p.EncodeValue(request)
					if err != nil || before != after {
						t.Fatal("enum preparation mutated the source", err)
					}
				}
			})
		}
	}
}

func TestOutputNumericEnumSixteenMappingDirections(t *testing.T) {
	for _, sourceCodec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(sourceCodec+"/"+targetCodec, func(t *testing.T) {
				source, request := decodeOutputEnumFixture(t, sourceCodec, outputNumericEnumSchema)
				target := testCompiled(t, targetCodec)
				prepared, err := prepareOutputConstraint(t, source, target, request, "compatible", false, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if (sourceCodec == Anthropic || sourceCodec == Gemini) && (targetCodec == Chat || targetCodec == Responses) {
					requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
					constraint, readErr := request.Parameters["outputConstraint"].ReadObject()
					if readErr != nil {
						t.Fatal(readErr)
					}
					constraint["name"] = p.StringValue("explicit_user_name")
					request.Parameters["outputConstraint"], err = p.EncodeValue(constraint)
					if err != nil {
						t.Fatal(err)
					}
					prepared, err = prepareOutputConstraint(t, source, target, request, "compatible", false, nil)
					if err != nil {
						t.Fatal(err)
					}
					wire, err = target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				}
				if err != nil {
					t.Fatal(err)
				}
				requireOutputEnumLexemes(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(targetCodec)...), outputNumericEnumSchema)
			})
		}
	}
}

func TestOutputNumericEnumRejectsInvalidNestedMembers(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, fixture := range []struct{ kind, enum, suffix string }{
			{"number", "null", ""}, {"number", "{}", ""}, {"number", "[]", ""},
			{"number", `[1,"2"]`, "/1"}, {"number", `[1,null]`, "/1"},
			{"number", `[false,2]`, "/0"}, {"number", `[1,{}]`, "/1"}, {"number", `[1,[]]`, "/1"},
			{"integer", `[1,1.5]`, "/1"}, {"integer", `[1,1e-2]`, "/1"},
			{"string", `[1]`, ""},
		} {
			t.Run(codec+"/"+fixture.kind+"/"+fixture.enum, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, Chat)
				schema := fmt.Sprintf(`{"type":"object","properties":{"a~/b":{"type":"array","items":{"type":%q,"enum":%s}}},"required":["a~/b"],"additionalProperties":false}`, fixture.kind, fixture.enum)
				setOutputConstraintSchema(t, request, schema)
				target := testCompiled(t, codec)
				_, err := encodeOutputEnum(t, source, target, request)
				suffix := fixture.suffix
				if fixture.kind == "string" && codec != Anthropic {
					suffix = "/0"
				}
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/properties/a~0~1b/items/enum"+suffix)
			})
		}
	}
}

func TestOutputNumericEnumUnrepresentedSchemaRemainsOwned(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, member := range []string{
			`{"type":"integer","const":9007199254740993}`,
			`{"type":"integer","enum":[1.5]}`,
			`{"type":"number","enum":[1,null]}`,
		} {
			t.Run(codec+"/"+member, func(t *testing.T) {
				schema := fmt.Sprintf(`{"type":"object","properties":{"answer":%s},"required":["answer"],"additionalProperties":false}`, member)
				compiled, request := decodeOutputEnumFixture(t, codec, schema)
				if !request.Parameters["outputConstraint"].IsZero() {
					t.Fatal("unrepresented schema was silently normalized")
				}
				prepared, err := prepareOutputConstraint(t, compiled, compiled, request, "compatible", false, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := compiled.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				actual := readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...)
				sameJSON(t, actual.Bytes(), schema)
			})
		}
	}
}
