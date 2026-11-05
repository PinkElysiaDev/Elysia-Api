package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPlannedCoreDecodingMatchesJSONBoundary(t *testing.T) {
	for _, direction := range []Direction{DecodeRequest, DecodeResponse} {
		for _, useIdentity := range []bool{false, true} {
			definition := testDefinition("planned-decode", "m", "h", "t", "r")
			if useIdentity {
				definition.Directions[direction] = Mapping{Transform: expressionPointer(readExpression("input", ""))}
			}
			compiled := compileTestDefinition(t, definition)
			if !compiled.mappings[direction].hasCoreOutputPlan {
				t.Fatal("core output plan was not selected")
			}
			for _, raw := range []string{
				`{"m":"model","request":"r","h":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello <world>"}]}],"r":[],"t":[]}`,
				`{"content":[{"kind":"text","payload":{"number":9007199254740993}}],"source":{},"schemaVersion":1}`,
				`{"schemaVersion":"wrong"}`, `{"content":{}}`, `{"unknown":true}`, `null`,
			} {
				input, err := ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				for _, options := range []EvaluationContext{{}, {Values: Object{"invalid": {}}}} {
					if direction == DecodeRequest {
						comparePlannedCoreDecode[Request](t, t.Context(), compiled, direction, input, options)
					} else {
						comparePlannedCoreDecode[Response](t, t.Context(), compiled, direction, input, options)
					}
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if direction == DecodeRequest {
				comparePlannedCoreDecode[Request](t, ctx, compiled, direction, Value{raw: "null"}, EvaluationContext{})
			} else {
				comparePlannedCoreDecode[Response](t, ctx, compiled, direction, Value{raw: "null"}, EvaluationContext{})
			}
		}
	}
}

func comparePlannedCoreDecode[T any](t *testing.T, ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext) *T {
	t.Helper()
	var want T
	output, wantErr := compiled.executeMapping(ctx, direction, input, options)
	if wantErr == nil {
		wantErr = decodeContract(output.Bytes(), &want)
	}
	got, gotErr := decodeTyped[T](ctx, compiled, direction, input, options, nil)
	var beforeLimit, afterLimit *ResourceLimitError
	var beforeConversion, afterConversion *ConversionError
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) ||
		errors.As(wantErr, &beforeLimit) != errors.As(gotErr, &afterLimit) || !reflect.DeepEqual(beforeLimit, afterLimit) ||
		errors.As(wantErr, &beforeConversion) != errors.As(gotErr, &afterConversion) ||
		errors.Is(wantErr, context.Canceled) != errors.Is(gotErr, context.Canceled) {
		t.Fatalf("%s boundary changed: got %T %v, want %T %v", direction, gotErr, gotErr, wantErr, wantErr)
	}
	if beforeConversion != nil && !reflect.DeepEqual(beforeConversion.Issues, afterConversion.Issues) {
		t.Fatal("conversion diagnostics changed", afterConversion.Issues, beforeConversion.Issues)
	}
	if wantErr == nil && (got == nil || !reflect.DeepEqual(*got, want)) {
		t.Fatalf("%s decoded value changed: got %#v, want %#v", direction, got, want)
	}
	return got
}

func TestPlannedOutputResourceBoundaries(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `[]`, `{"unknown":{"a":[null,{},[],{"b":"escaped\\\"<>"}]},"unknown":true}`,
	} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []JSONOutput{JSONLeaf(value), expandCoreOutput(t, value)} {
			encoded, err := output.Encode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			planned := plannedOutput{output, encoded.Len()}
			for _, metric := range []string{"bytes", "nodes", "depth", "combined"} {
				for limit := 0; limit <= encoded.Len()+1; limit++ {
					limits := DefaultLimits()
					switch metric {
					case "bytes":
						limits.BufferBytes = limit
					case "nodes":
						limits.Nodes = limit
					case "depth":
						limits.Depth = limit
					case "combined":
						limits.BufferBytes, limits.Nodes, limits.Depth = limit, limit, limit
					}
					want := checkResourceValue(encoded, limits, "mapping_output")
					got := checkPlannedOutputLimits(planned, limits, "mapping_output")
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s limit %d on %s: got %v, want %v", metric, limit, raw, got, want)
					}
				}
			}
		}
	}

	definition := testDefinition("planned-output-budget", "m", "h", "t", "r")
	definition.Directions[DecodeRequest] = Mapping{Transform: expressionPointer(Expression{Op: "literal", Value: Value{raw: `{"unknown":[1,2,3,4,5,6,7,8,9,10,11,12,13]}`}})}
	compiled := compileTestDefinition(t, definition)
	compiled.limits.Nodes = 1
	comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, Value{raw: "null"}, EvaluationContext{})
	_, err := decodeTyped[Request](t.Context(), compiled, DecodeRequest, Value{raw: "null"}, EvaluationContext{}, nil)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Domain != "mapping_output" {
		t.Fatal("unknown fields were decoded before output budget validation", err)
	}
}

func TestPlannedCoreDecodingSchemaAndPackageBoundaries(t *testing.T) {
	definition := testDefinition("planned-schema", "m", "h", "t", "r")
	definition.Directions[DecodeRequest] = Mapping{
		Input:     &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"model": {Type: StringType}}, Required: []string{"model"}, AllowUnknown: true},
		Transform: expressionPointer(readExpression("input", "")),
	}
	compiled := compileTestDefinition(t, definition)
	if !compiled.mappings[DecodeRequest].hasCoreOutputPlan {
		t.Fatal("wire input schema unnecessarily disabled the core writer")
	}
	comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, Value{raw: `{}`}, EvaluationContext{})

	mapping := definition.Directions[DecodeRequest]
	mapping.Output = &ValueSchema{Type: ObjectType, AllowUnknown: true}
	definition.Directions[DecodeRequest] = mapping
	withOutput := compileTestDefinition(t, definition)
	if withOutput.mappings[DecodeRequest].hasCoreOutputPlan {
		t.Fatal("semantic output schema bypassed its JSON boundary")
	}

	document := textProtocolPackage(t)
	direction := document.Mapping.Directions[DecodeRequest]
	direction.Program.Fields["schemaVersion"] = literalExpression(1)
	document.Mapping.Directions[DecodeRequest] = direction
	compiled = compilePackageForTest(t, document)
	comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, Value{raw: `{"model":"m","prompt":"hello"}`}, EvaluationContext{})
}

func TestPlannedCoreDecodingOwnsMutableContainers(t *testing.T) {
	definition := testDefinition("planned-ownership", "m", "h", "t", "r")
	definition.Directions[DecodeRequest] = Mapping{Transform: expressionPointer(readExpression("input", ""))}
	compiled := compileTestDefinition(t, definition)
	input := Value{raw: `{"content":[{"kind":"text","payload":"text"}],"parameters":{"small":"value"}}`}
	first := comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, input, EvaluationContext{})
	second := comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, input, EvaluationContext{})
	first.Content[0].Payload = StringValue("changed")
	first.Parameters["small"] = StringValue("changed")
	if second.Content[0].Payload != StringValue("text") || second.Parameters["small"] != StringValue("value") {
		t.Fatal("decodes share mutable containers")
	}
	comparePlannedCoreDecode[Request](t, t.Context(), compiled, DecodeRequest, input, EvaluationContext{})
}

func BenchmarkPlannedCoreDecoding(b *testing.B) {
	definition := testDefinition("planned-decode-bench", "m", "h", "t", "r")
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	value, _ := EncodeValue(definition)
	compiled, issues := compiler.Compile(value.Bytes())
	if err := IssuesError(issues); err != nil {
		b.Fatal(err)
	}
	input := Value{raw: `{"m":"model","h":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"` + strings.Repeat("x", 2<<20) + `"}]}]}`}
	for _, hasPlan := range []bool{false, true} {
		name := "json-boundary"
		if hasPlan {
			name = "core-plan"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var request *Request
				var err error
				if hasPlan {
					request, err = decodePlannedCore[Request](b.Context(), compiled, DecodeRequest, input, EvaluationContext{})
				} else {
					var output Value
					output, err = compiled.executeMapping(b.Context(), DecodeRequest, input, EvaluationContext{})
					if err == nil {
						request = new(Request)
						err = decodeContract(output.Bytes(), request)
					}
				}
				if err != nil || request == nil || len(request.Content) != 1 {
					b.Fatal("incomplete core request", err)
				}
			}
		})
	}
}
