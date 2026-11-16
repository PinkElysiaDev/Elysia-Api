package builtin

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	outputConstraintSampleID     = "output-constraint-decode"
	outputConstraintSchema       = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
	outputConstraintEditedSchema = `{"type":"object","properties":{"answer":{"type":"array","items":{"type":"boolean"}}},"required":["answer"],"additionalProperties":false}`
	outputConstraintCorePath     = "/parameters/outputConstraint"
)

func decodeOutputConstraintFixture(t *testing.T, codec string) (*p.Compiled, *p.Request) {
	t.Helper()
	compiled := testCompiled(t, codec)
	samples := packageMapping(compiled).Samples
	index := slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })
	if index < 0 {
		t.Fatal("missing independent output constraint sample", codec)
	}
	sample := samples[index]
	request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), fixtureOptions(p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}))
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	clearEvidenceProvenance(request.Content)
	return compiled, request
}

func setOutputConstraintSchema(t *testing.T, request *p.Request, schema string) {
	t.Helper()
	fields, err := request.Parameters["outputConstraint"].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	fields["schema"] = testValue(t, schema)
	request.Parameters["outputConstraint"], err = p.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
}

func editOutputWire(t *testing.T, value p.Value, path []string, replacement p.Value) p.Value {
	t.Helper()
	if len(path) == 0 {
		return replacement
	}
	fields, err := value.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	updated := editOutputWire(t, fields[path[0]], path[1:], replacement)
	if updated.IsZero() {
		delete(fields, path[0])
	} else {
		fields[path[0]] = updated
	}
	result, err := p.EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func outputFormatPath(codec string) []string {
	switch codec {
	case Chat:
		return []string{"response_format", "json_schema"}
	case Responses:
		return []string{"text", "format"}
	case Anthropic:
		return []string{"output_config", "format"}
	default:
		return []string{"generationConfig"}
	}
}

func readOutputWire(t *testing.T, value p.Value, path ...string) p.Value {
	t.Helper()
	for _, name := range path {
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		value = fields[name]
	}
	return value
}

func requireOutputConstraintIssue(t *testing.T, err error, path string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || !slices.ContainsFunc(failure.Issues, func(issue p.ConversionIssue) bool {
		return issue.Path == path && (issue.Code == p.UnsupportedCapability || issue.Code == p.ConversionRejected)
	}) {
		t.Fatal("missing precise output constraint refusal", path, err)
	}
}

func prepareOutputConstraint(t *testing.T, source, target *p.Compiled, request *p.Request, mode string, isDisabled bool, sink *p.DiagnosticSink) (*p.Request, error) {
	t.Helper()
	policy := p.DefaultConversionPolicy(source, target)
	policy.Mode = mode
	if isDisabled {
		for index := range policy.Rules {
			if strings.HasPrefix(policy.Rules[index].ID, "output-constraint-") {
				policy.Rules[index].Enabled = false
			}
		}
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	return conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
}

func TestOutputConstraintIndependentEditedInverse(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			source, request := decodeOutputConstraintFixture(t, codec)
			fields, err := request.Parameters["outputConstraint"].ReadObject()
			if err != nil || !fields["kind"].IsString("json-schema") || !fields["dialect"].IsString("json-schema") || !fields["enforcement"].IsString("required") {
				t.Fatal("native format did not become neutral intent", fields, err)
			}
			sameJSON(t, fields["schema"].Bytes(), outputConstraintSchema)
			setOutputConstraintSchema(t, request, outputConstraintEditedSchema)
			before, _ := p.EncodeValue(request)
			prepared, err := prepareOutputConstraint(t, source, source, request, "compatible", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := source.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := source.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, decoded.Parameters["outputConstraint"].Bytes(), request.Parameters["outputConstraint"].JSONString())
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("target preparation changed the source constraint")
			}
		})
	}
}

func TestOutputConstraintSixteenMappingDirections(t *testing.T) {
	for _, sourceCodec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(sourceCodec+"/"+targetCodec, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, sourceCodec)
				target := testCompiled(t, targetCodec)
				prepared, err := prepareOutputConstraint(t, source, target, request, "compatible", false, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if (sourceCodec == Anthropic || sourceCodec == Gemini) && (targetCodec == Chat || targetCodec == Responses) {
					requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := target.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, decoded.Parameters["outputConstraint"].Bytes(), prepared.Parameters["outputConstraint"].JSONString())
			})
		}
	}
}

func TestOutputConstraintNestedKeywordBoundaries(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, keyword := range []string{`"minimum":9007199254740993`, `"oneOf":[{"type":"string"},{"type":"string"}]`, `"enum":["Case"]`, `"nullable":true`, `"const":9007199254740993`, `"$ref":"#/$defs/value"`} {
			t.Run(codec+"/"+keyword, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, Chat)
				target := testCompiled(t, codec)
				schema := `{"type":"object","properties":{"a~/b":{"type":"string",` + keyword + `}},"required":["a~/b"],"additionalProperties":false}`
				setOutputConstraintSchema(t, request, schema)
				wire, err := encodeOutputEnum(t, source, target, request)
				key := strings.SplitN(keyword, `"`, 3)[1]
				if key == "enum" && codec != Anthropic {
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...).Bytes(), schema)
					return
				}
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/properties/a~0~1b/"+key)
			})
		}
	}
}

func TestOutputConstraintHintsRequireNamedPermission(t *testing.T) {
	for _, targetCodec := range []string{Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(targetCodec+"/"+mode, func(t *testing.T) {
				source, request := decodeOutputConstraintFixture(t, Chat)
				target := testCompiled(t, targetCodec)
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
				_, err = target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if mode == "disabled" {
					requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
					return
				}
				if err != nil || !slices.ContainsFunc(sink.Issues(), func(issue p.ConversionIssue) bool {
					return issue.RuleID == "output-constraint-name" && issue.Fidelity == "lossy_compatible"
				}) {
					t.Fatal("hint loss was not independently diagnosed", sink.Issues(), err)
				}
			})
		}
	}
}

func TestOutputConstraintStringEnumOwnership(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			schema := strings.Replace(outputConstraintSchema, `"type":"string"`, `"type":"string","enum":["ExactCase"]`, 1)
			compiled, request := decodeOutputEnumFixture(t, codec, schema)
			hasNeutralConstraint := !request.Parameters["outputConstraint"].IsZero()
			if hasNeutralConstraint == (codec == Anthropic) {
				t.Fatal("string enum has the wrong semantic owner", request.Parameters)
			}
			wire, err := encodeOutputEnum(t, compiled, compiled, request)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...).Bytes(), schema)
			otherCodec := Chat
			if codec == Chat {
				otherCodec = Responses
			}
			other := testCompiled(t, otherCodec)
			wire, err = encodeOutputEnum(t, compiled, other, request)
			if codec == Anthropic {
				if err == nil {
					t.Fatal("source-owned string enum crossed protocols")
				}
				return
			}
			if codec == Gemini {
				requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/name")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(otherCodec)...).Bytes(), schema)
		})
	}
}

func TestOutputConstraintCountTokensRemainsIndependent(t *testing.T) {
	for _, codec := range []string{Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			compiled, request := decodeOutputConstraintFixture(t, codec)
			counter, ok := compiled.TokenCountRequestProtocol("count_tokens")
			if !ok {
				t.Fatal("missing independent count request protocol")
			}
			_, err := counter.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath)
		})
	}
}

func TestOutputConstraintDoesNotStrengthenOptionalFields(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			compiled, request := decodeOutputConstraintFixture(t, codec)
			schema := strings.Replace(outputConstraintSchema, `"required":["answer"]`, `"required":[]`, 1)
			setOutputConstraintSchema(t, request, schema)
			_, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/schema/required")
		})
	}
}

func TestOutputConstraintExplicitStrictStates(t *testing.T) {
	testOutputConstraintExplicitStrictStates(t, outputConstraintSchema)
}

func testOutputConstraintExplicitStrictStates(t *testing.T, schema string) {
	t.Helper()
	for _, codec := range []string{Chat, Responses} {
		for _, strict := range []string{"missing", "null", "false", "true"} {
			t.Run(fmt.Sprintf("%s/%s", codec, strict), func(t *testing.T) {
				compiled := testCompiled(t, codec)
				samples := packageMapping(compiled).Samples
				sample := samples[slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })]
				var replacement p.Value
				if strict != "missing" {
					replacement = testValue(t, strict)
				}
				body := editOutputWire(t, sample.Input, outputSchemaPath(codec), testValue(t, schema))
				body = editOutputWire(t, body, append(outputFormatPath(codec), "strict"), replacement)
				request, err := compiled.DecodeRequest(t.Context(), body.Bytes(), fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				constraint := request.Parameters["outputConstraint"]
				if strict == "missing" || strict == "null" {
					if !constraint.IsZero() {
						t.Fatal("unknown implicit enforcement became an explicit boolean", constraint)
					}
				} else {
					fields, err := constraint.ReadObject()
					want := "required"
					if strict == "false" {
						want = "preferred"
					}
					if err != nil || !fields["enforcement"].IsString(want) {
						t.Fatal("explicit enforcement changed", fields, err)
					}
				}
				request.Native = nil
				clearEvidenceProvenance(request.Content)
				prepared, err := prepareOutputConstraint(t, compiled, compiled, request, "compatible", false, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := compiled.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				got := readOutputWire(t, testValue(t, string(wire)), append(outputFormatPath(codec), "strict")...)
				if strict == "missing" && !got.IsZero() || strict != "missing" && got.JSONString() != strict {
					t.Fatal("own reconstruction changed explicit enforcement presence", strict, got)
				}
				sameJSON(t, readOutputWire(t, testValue(t, string(wire)), outputSchemaPath(codec)...).Bytes(), schema)
			})
		}
	}
}

func TestOutputConstraintSiblingOwnership(t *testing.T) {
	for _, test := range []struct {
		codec string
		path  []string
		value string
	}{
		{Responses, []string{"text", "verbosity"}, `"low"`},
		{Anthropic, []string{"output_config", "future"}, `{"keep":9007199254740993}`},
		{Gemini, []string{"generationConfig", "future"}, `{"keep":9007199254740993}`},
	} {
		t.Run(test.codec, func(t *testing.T) {
			compiled := testCompiled(t, test.codec)
			samples := packageMapping(compiled).Samples
			sample := samples[slices.IndexFunc(samples, func(sample p.Sample) bool { return sample.ID == outputConstraintSampleID })]
			body := editOutputWire(t, sample.Input, test.path, testValue(t, test.value))
			request, err := compiled.DecodeRequest(t.Context(), body.Bytes(), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			clearEvidenceProvenance(request.Content)
			setOutputConstraintSchema(t, request, outputConstraintEditedSchema)
			prepared, err := prepareOutputConstraint(t, compiled, compiled, request, "compatible", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, readOutputWire(t, testValue(t, string(wire)), test.path...).Bytes(), test.value)
			decoded, err := compiled.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, decoded.Parameters["outputConstraint"].Bytes(), prepared.Parameters["outputConstraint"].JSONString())
		})
	}
}

func TestOutputConstraintRejectsDuplicateOwners(t *testing.T) {
	for _, test := range []struct {
		codec, owner, remainder string
	}{
		{Chat, "wire:openai_chat", `{"response_format":{"type":"json_object"}}`},
		{Responses, "wire:openai_responses", `{"text":{"format":{"type":"json_object"}}}`},
		{Anthropic, "wire:claude", `{"output_config":{"format":{"type":"json_schema","schema":{}}}}`},
		{Gemini, "wire:gemini", `{"generationConfig":{"responseMimeType":"application/json"}}`},
	} {
		t.Run(test.codec, func(t *testing.T) {
			compiled, request := decodeOutputConstraintFixture(t, test.codec)
			request.Parameters[test.owner] = testValue(t, test.remainder)
			_, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath)
		})
	}
}

func TestOutputConstraintDoesNotPromotePreferredEnforcement(t *testing.T) {
	source, request := decodeOutputConstraintFixture(t, Chat)
	request.Parameters["outputConstraint"] = editOutputWire(t, request.Parameters["outputConstraint"], []string{"enforcement"}, p.StringValue("preferred"))
	for _, codec := range []string{Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			target := testCompiled(t, codec)
			prepared, err := prepareOutputConstraint(t, source, target, request, "compatible", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = target.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/enforcement")
		})
	}
}

func TestOutputConstraintEvidenceKeepsTextProfiles(t *testing.T) {
	for _, codec := range []string{Anthropic, Gemini} {
		t.Run(codec, func(t *testing.T) {
			source, target := testCompiled(t, codec), testCompiled(t, Chat)
			contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true, p.OutputConstraintsCapability: true}
			reports := p.VerifyBindingProfiles(t.Context(), source, target, contract)
			if len(reports) == 0 || reports[0].Passed {
				t.Fatal("nameless schema unexpectedly passed the complete target contract", reports)
			}
			hasText := slices.ContainsFunc(reports, func(report p.CombinationReport) bool {
				return report.Passed && report.Capabilities[p.TextCapability] && !report.Capabilities[p.OutputConstraintsCapability]
			})
			if !hasText {
				t.Fatal("schema refusal destroyed unrelated text-only route evidence", reports)
			}
			_, request := decodeOutputConstraintFixture(t, codec)
			issues := p.CheckRequest(request, p.Target{Capabilities: p.CapabilitySet{p.TextCapability: true}}, p.DefaultLimits())
			if !slices.ContainsFunc(issues, func(issue p.ConversionIssue) bool {
				return issue.Capability == p.OutputConstraintsCapability && issue.Path == outputConstraintCorePath
			}) {
				t.Fatal("text permission authorized an output constraint", issues)
			}
		})
	}
}

func TestOutputConstraintLegacyNativeForms(t *testing.T) {
	for _, test := range []struct{ codec, body string }{
		{Chat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object","properties":{"answer":{"type":"string"}}}}}}`},
		{Gemini, `{"contents":[],"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"OBJECT","properties":{"answer":{"type":"STRING"}}}}}`},
	} {
		t.Run(test.codec, func(t *testing.T) {
			compiled := testCompiled(t, test.codec)
			request, err := compiled.DecodeRequest(t.Context(), []byte(test.body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if !request.Parameters["outputConstraint"].IsZero() {
				t.Fatal("unsupported dialect or implicit enforcement was relabeled as neutral", request.Parameters)
			}
			request.Native = nil
			clearEvidenceProvenance(request.Content)
			wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, test.body)
		})
	}
}

func TestOutputConstraintJSONContainerBoundary(t *testing.T) {
	for _, test := range []struct{ codec, kind string }{
		{Chat, "json"}, {Responses, "json"}, {Gemini, "json-object"},
	} {
		t.Run(test.codec+"/"+test.kind, func(t *testing.T) {
			compiled, request := decodeOutputConstraintFixture(t, test.codec)
			request.Parameters["outputConstraint"] = testValue(t, `{"version":1,"kind":"`+test.kind+`","enforcement":"required"}`)
			_, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			requireOutputConstraintIssue(t, err, outputConstraintCorePath+"/kind")
		})
	}
}
