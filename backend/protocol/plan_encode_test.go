package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPlannedCoreEncodingMatchesJSONBoundary(t *testing.T) {
	request := &Request{SchemaVersion: SemanticSchemaVersion, Model: StringValue("model"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello <world>")}}}}, Tools: []Tool{}, Parameters: Object{"large": {raw: "9007199254740993"}, "null": {raw: "null"}, "invalid\xff": StringValue("replacement"), "invalid�": StringValue("last"), "spaced": {raw: ` { "a" : 1e+3 } `}}}
	response := &Response{SchemaVersion: SemanticSchemaVersion, ID: StringValue("r"), Content: request.Content, Usage: &Usage{Input: &Counter{Count: 0, Origin: ObservedCount}}}
	for _, document := range []struct {
		direction Direction
		input     any
	}{
		{EncodeRequest, request}, {EncodeRequest, (*Request)(nil)}, {EncodeResponse, response},
	} {
		for _, expression := range []*Expression{
			nil,
			expressionPointer(readExpression("input", "")),
			expressionPointer(objectExpression(map[string]Expression{"input": readExpression("root", ""), "field": readExpression("input", "/parameters/spaced"), "replacement": readExpression("input", "/parameters/invalid�")})),
			expressionPointer(Expression{Op: "merge", Policy: "reject", Items: []Expression{readExpression("input", ""), readExpression("input", "")}}),
			expressionPointer(Expression{Op: "merge", Policy: "reject", Items: []Expression{readExpression("input", "/parameters")}}),
			expressionPointer(Expression{Op: "merge", Policy: "first", Items: []Expression{readExpression("input", "/parameters"), literalExpression(map[string]string{"invalid�": "other"})}}),
		} {
			definition := testDefinition("planned-core", "m", "h", "t", "r")
			if expression != nil {
				definition.Directions[document.direction] = Mapping{Transform: expression}
			}
			compiled := compileTestDefinition(t, definition)
			for _, limit := range []Limits{DefaultLimits(), {Depth: 1, Nodes: 1000, BufferBytes: 1000}, {Depth: 64, Nodes: 20, BufferBytes: 1000}, {Depth: 64, Nodes: 1000, BufferBytes: 10}} {
				compiled.limits = limit
				options := EvaluationContext{Values: Object{"test": StringValue("context")}}
				var want Value
				encoded, wantErr := EncodeSemantic(t.Context(), document.direction, document.input)
				if wantErr == nil {
					want, wantErr = compiled.executeMapping(t.Context(), document.direction, encoded, options)
				}
				got, gotErr := encodePlannedCore(t.Context(), compiled, document.direction, document.input, options)
				var before, after *ResourceLimitError
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || errors.As(wantErr, &before) != errors.As(gotErr, &after) || !reflect.DeepEqual(before, after) {
					t.Fatalf("%s limits=%+v: got %s / %v, want %s / %v", document.direction, limit, got.raw, gotErr, want.raw, wantErr)
				}
			}
		}
	}
	compiled := compileTestDefinition(t, testDefinition("planned-cancel", "m", "h", "t", "r"))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := encodePlannedCore(cancelled, compiled, EncodeRequest, request, EvaluationContext{}); !errors.Is(err, context.Canceled) {
		t.Fatal("core input plan lost cancellation", err)
	}
}

func BenchmarkPlannedCoreEncoding(b *testing.B) {
	definition := testDefinition("planned-bench", "m", "h", "t", "r")
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	value, _ := EncodeValue(definition)
	compiled, issues := compiler.Compile(value.Bytes())
	if err := IssuesError(issues); err != nil {
		b.Fatal(err)
	}
	input := &Request{SchemaVersion: SemanticSchemaVersion, Model: StringValue("model"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue(strings.Repeat("x", 2<<20))}}}}}
	for _, hasPlan := range []bool{false, true} {
		name := "json-boundary"
		if hasPlan {
			name = "core-plan"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var output Value
				var err error
				if hasPlan {
					output, err = encodePlannedCore(b.Context(), compiled, EncodeRequest, input, EvaluationContext{})
				} else {
					output, err = EncodeSemantic(b.Context(), EncodeRequest, input)
					if err == nil {
						output, err = compiled.executeMapping(b.Context(), EncodeRequest, output, EvaluationContext{})
					}
				}
				if err != nil || output.Len() < 2<<20 {
					b.Fatal("incomplete wire", err)
				}
			}
		})
	}
}
