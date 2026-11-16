package builtin

import (
	"fmt"
	"slices"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	outputStringEnumSchema  = `{"type":"object","properties":{"answer":{"type":"string","enum":["ExactCase","exactcase"," 大自在天 ","","é","e\u0301","😀","a~/b","ExactCase"]}},"required":["answer"],"additionalProperties":false}`
	outputStringFixedSchema = `{"type":"object","properties":{"answer":{"type":"string","enum":[""]}},"required":["answer"],"additionalProperties":false}`
)

func TestOutputStringEnumNativeAndEditedInverse(t *testing.T) {
	testOutputEnumNativeAndEditedInverse(t, []string{Chat, Responses, Gemini}, map[string]string{
		"unicode-case-order": outputStringEnumSchema, "empty-string-singleton": outputStringFixedSchema,
	}, outputStringFixedSchema)
}

func TestOutputStringEnumTargetsAndNames(t *testing.T) {
	for _, sourceCodec := range []string{Chat, Responses, Gemini} {
		for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(sourceCodec+"/"+targetCodec, func(t *testing.T) {
				source, request := decodeOutputEnumFixture(t, sourceCodec, outputStringEnumSchema)
				target := testCompiled(t, targetCodec)
				wire, err := encodeOutputEnum(t, source, target, request)
				if targetCodec == Anthropic {
					requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/properties/answer/enum")
					return
				}
				if sourceCodec == Gemini && targetCodec != Gemini {
					requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
					request.Parameters["outputConstraint"] = editOutputWire(t, request.Parameters["outputConstraint"], []string{"name"}, p.StringValue("explicit_user_name"))
					wire, err = encodeOutputEnum(t, source, target, request)
				}
				if err != nil {
					t.Fatal(err)
				}
				requireOutputEnumLexemes(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(targetCodec)...), outputStringEnumSchema)
			})
		}
	}
}

func TestOutputStringEnumNestedSchemaIsData(t *testing.T) {
	const schema = `{"type":"object","title":"enum and properties are ordinary words","properties":{"enum":{"type":"string","enum":["enum","properties"]},"a~/b":{"type":"array","items":{"type":"string","description":"keep enum","enum":["Case","case","Case"]}}},"required":["a~/b","enum"],"additionalProperties":false}`
	for _, codec := range []string{Chat, Responses, Gemini} {
		t.Run(codec, func(t *testing.T) {
			compiled, request := decodeOutputEnumFixture(t, codec, schema)
			sameJSON(t, readOutputWire(t, request.Parameters["outputConstraint"], "schema").Bytes(), schema)
			wire, err := encodeOutputEnum(t, compiled, compiled, request)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...).Bytes(), schema)
		})
	}
}

func TestOutputStringEnumAnthropicNativeOwnership(t *testing.T) {
	source, request := decodeOutputEnumFixture(t, Anthropic, outputStringEnumSchema)
	if !request.Parameters["outputConstraint"].IsZero() {
		t.Fatal("native casing limitation became an exact neutral promise")
	}
	for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(targetCodec, func(t *testing.T) {
			target := testCompiled(t, targetCodec)
			wire, err := encodeOutputEnum(t, source, target, request)
			if targetCodec != Anthropic {
				if err == nil {
					t.Fatal("source-owned enum crossed protocols")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requireOutputEnumLexemes(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(targetCodec)...), outputStringEnumSchema)
		})
	}
}

func TestOutputStringEnumRejectsInvalidNestedMembers(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Gemini} {
		for _, fixture := range []struct{ enum, suffix string }{
			{"null", ""}, {"{}", ""}, {"[]", ""}, {`"Case"`, ""},
			{`["Case",null]`, "/1"}, {`["Case",1]`, "/1"}, {`[false,"Case"]`, "/0"},
			{`["Case",{}]`, "/1"}, {`["Case",[]]`, "/1"}, {`["Case",null,1]`, "/1"},
		} {
			t.Run(codec+"/"+fixture.enum, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, Chat)
				schema := fmt.Sprintf(`{"type":"object","properties":{"a~/b":{"type":"array","items":{"type":"string","enum":%s}}},"required":["a~/b"],"additionalProperties":false}`, fixture.enum)
				setOutputConstraintSchema(t, request, schema)
				_, err := encodeOutputEnum(t, source, testCompiled(t, codec), request)
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/properties/a~0~1b/items/enum"+fixture.suffix)
			})
		}
	}
}

func TestOutputStringEnumPreservesDiagnosticOrder(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Gemini} {
		for _, fixture := range []struct{ member, suffix string }{
			{`null`, ""},
			{`{"enum":null}`, "/type"},
			{`{"type":null,"enum":null}`, "/type"},
			{`{"type":[],"enum":null}`, "/type"},
			{`{"type":"unknown","enum":null}`, "/type"},
			{`{"type":"string","enum":null,"":true}`, "/"},
			{`{"type":"boolean","enum":[true]}`, "/enum"},
			{`{"type":"null","enum":[null]}`, "/enum"},
			{`{"type":"string","enum":null,"title":false,"z~/x":true,"a~/b":true}`, "/a~0~1b"},
			{`{"type":"string","enum":null,"title":false,"description":false}`, "/title"},
			{`{"type":"string","enum":null,"title":"kept","description":false}`, "/description"},
			{`{"type":"string","enum":null,"title":"kept","description":"enum is an ordinary word"}`, "/enum"},
			{`{"type":"object","properties":{},"required":null,"additionalProperties":true}`, "/required"},
			{`{"type":"object","properties":{},"required":[1],"additionalProperties":true}`, "/additionalProperties"},
			{`{"type":"object","properties":{},"required":[1],"additionalProperties":false}`, "/required"},
			{`{"type":"object","properties":{},"required":["missing"],"additionalProperties":false}`, "/required"},
		} {
			t.Run(codec+"/"+fixture.member, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, Chat)
				schema := fmt.Sprintf(`{"type":"object","properties":{"a~/b":%s},"required":["a~/b"],"additionalProperties":false}`, fixture.member)
				setOutputConstraintSchema(t, request, schema)
				_, err := encodeOutputEnum(t, source, testCompiled(t, codec), request)
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/properties/a~0~1b"+fixture.suffix)
			})
		}
	}
}

func TestOutputStringEnumUnsupportedNeighborsRemainOwned(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Gemini} {
		for _, member := range []string{
			`{"type":"string","enum":["ExactCase"],"const":"ExactCase"}`,
			`{"type":"string","enum":["ExactCase"],"pattern":"^ExactCase$"}`,
			`{"type":"string","enum":null}`,
			`{"type":"string","enum":["ExactCase",1]}`,
		} {
			t.Run(codec+"/"+member, func(t *testing.T) {
				schema := fmt.Sprintf(`{"type":"object","properties":{"answer":%s},"required":["answer"],"additionalProperties":false}`, member)
				compiled, request := decodeOutputEnumFixture(t, codec, schema)
				if !request.Parameters["outputConstraint"].IsZero() {
					t.Fatal("unrepresented source constraint became neutral")
				}
				wire, err := encodeOutputEnum(t, compiled, compiled, request)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...).Bytes(), schema)
			})
		}
	}
}

func TestOutputStringEnumHintPermissions(t *testing.T) {
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			source, request := decodeOutputEnumFixture(t, Chat, outputStringEnumSchema)
			request.Parameters["outputConstraint"] = editOutputWire(t, request.Parameters["outputConstraint"], []string{"description"}, p.StringValue("caller hint"))
			target := testCompiled(t, Gemini)
			sink := &p.DiagnosticSink{}
			policyMode := mode
			if mode == "disabled" {
				policyMode = "compatible"
			}
			prepared, err := prepareOutputConstraint(t, source, target, request, policyMode, mode == "disabled", sink)
			if mode == "strict" {
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
			if mode == "disabled" {
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, rule := range []string{"output-constraint-name", "output-constraint-description"} {
				if !slices.ContainsFunc(sink.Issues(), func(issue p.ConversionIssue) bool {
					return issue.RuleID == rule && issue.Fidelity == "lossy_compatible"
				}) {
					t.Fatal("hint loss lacks its own permission and diagnostic", rule, sink.Issues())
				}
			}
			requireOutputEnumLexemes(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(Gemini)...), outputStringEnumSchema)
		})
	}
}

func TestOutputStringEnumExplicitStrictStates(t *testing.T) {
	testOutputConstraintExplicitStrictStates(t, outputStringEnumSchema)
}

func TestOutputStringEnumCapabilityAndCountTokens(t *testing.T) {
	source, request := decodeOutputEnumFixture(t, Chat, outputStringEnumSchema)
	issues := p.CheckRequest(request, p.Target{Capabilities: p.CapabilitySet{p.TextCapability: true}}, p.DefaultLimits())
	if !slices.ContainsFunc(issues, func(issue p.ConversionIssue) bool {
		return issue.Capability == p.OutputConstraintsCapability && issue.Path == outputConstraintCorePath
	}) {
		t.Fatal("text permission authorized a string enum", issues)
	}
	for _, codec := range []string{Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			target := testCompiled(t, codec)
			counter, ok := target.TokenCountRequestProtocol("count_tokens")
			if !ok {
				t.Fatal("missing independent count request protocol")
			}
			_, err := counter.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath)
		})
	}
	request.Parameters["outputConstraint"] = editOutputWire(t, request.Parameters["outputConstraint"], []string{"enforcement"}, p.StringValue("preferred"))
	_, err := encodeOutputEnum(t, source, testCompiled(t, Gemini), request)
	requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/enforcement")
}
