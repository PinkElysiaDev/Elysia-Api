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
	event := &Event{SchemaVersion: SemanticSchemaVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: request.Content[0].Children[0].Payload, Usage: response.Usage}
	for _, document := range []struct {
		direction Direction
		input     any
	}{
		{EncodeRequest, request}, {EncodeRequest, (*Request)(nil)}, {EncodeResponse, response}, {EncodeEvent, event},
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
			if document.direction == EncodeEvent && expression == nil {
				expression = expressionPointer(objectExpression(map[string]Expression{"type": readExpression("input", "/type"), "text": readExpression("input", "/delta")}))
			}
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
	compiled.limits.Depth = 1
	options := EvaluationContext{Values: Object{"invalid": {}}}
	encoded, err := EncodeSemantic(t.Context(), EncodeResponse, response)
	if err != nil {
		t.Fatal(err)
	}
	_, wantErr := compiled.executeMapping(t.Context(), EncodeResponse, encoded, options)
	_, gotErr := encodePlannedCore(t.Context(), compiled, EncodeResponse, response, options)
	var before, after *ResourceLimitError
	if !errors.As(wantErr, &before) || !errors.As(gotErr, &after) || !reflect.DeepEqual(before, after) || after.Domain != "mapping_input" || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatal("tighter mapping budget lost precedence over invalid context encoding", gotErr, wantErr)
	}
	if _, err := encodePlannedCore(cancelled, compiled, EncodeResponse, response, options); !errors.Is(err, context.Canceled) {
		t.Fatal("mapping budget preceded cancellation", err)
	}
}

func TestPlannedCoreReadsShareLargeValuesAndDetachSmallResults(t *testing.T) {
	payload := Value{raw: `{"data":"` + strings.Repeat("A", 2<<20) + `","fragment":"` + strings.Repeat("b", 90<<10) + `","name":"small"}`}
	request := &Request{SchemaVersion: SemanticSchemaVersion, Content: []Node{{Kind: ImageNode, Payload: payload}}}
	size, err := measureSemanticValue(request, semanticLimits(DefaultLimits()))
	if err != nil || size.maxRawBytes != payload.Len() {
		t.Fatal("semantic measurement lost the largest raw owner", size, err)
	}
	for _, tc := range []struct {
		name, field string
		isBinding   bool
		shouldShare bool
	}{
		{"direct-large", "data", false, true},
		{"direct-small", "name", false, false},
		{"direct-small-fraction", "fragment", false, false},
		{"binding-large", "data", true, true},
		{"binding-small", "name", true, false},
		{"binding-small-fraction", "fragment", true, false},
		{"whole-value", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/content/0/payload"
			if tc.field != "" {
				path += "/" + tc.field
			}
			program := readExpression("input", path)
			if tc.isBinding {
				read := bindingTestRead("node", "/payload/"+tc.field)
				program = Expression{Op: "let", Name: "node", Source: expressionPointer(readExpression("input", "/content/0")), Body: &Expression{Op: "if", When: &Expression{Op: "exists", Source: &read}, Then: &read}}
			}
			definition := testDefinition("planned-read-ownership", "m", "h", "t", "r")
			definition.Directions[EncodeRequest] = Mapping{Transform: &program}
			compiled := compileTestDefinition(t, definition)
			output, err := encodePlannedCore(t.Context(), compiled, EncodeRequest, request, EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			want := payload
			if tc.field != "" {
				want, err = readValuePointer(payload, []string{tc.field})
			}
			if err != nil || output != want || sharesValueStorage(payload, output) != tc.shouldShare {
				t.Fatal("planned read changed bytes or retained the wrong storage", err)
			}
		})
	}
}

func TestPlannedCoreConstructedParentDetachesRootSelection(t *testing.T) {
	fragment := StringValue(strings.Repeat("b", 90<<10))
	payload := Value{raw: `{"fragment":` + fragment.raw + `,"data":"` + strings.Repeat("A", 2<<20) + `"}`}
	for _, isIteration := range []bool{false, true} {
		source := Expression{Op: "literal", Value: payload}
		body := bindingTestRead("selected", "/fragment")
		if isIteration {
			source = Expression{Op: "map", Source: &Expression{Op: "literal", Value: Value{raw: "[" + payload.raw + "]"}}, Body: expressionPointer(readExpression("item", "/fragment"))}
			body = bindingTestRead("selected", "/0")
		}
		program := Expression{Op: "let", Name: "selected", Source: &source, Body: &body}
		definition := testDefinition("constructed-parent", "m", "h", "t", "r")
		definition.Directions[EncodeRequest] = Mapping{Transform: &program}
		compiled := compileTestDefinition(t, definition)
		parent := compiled.mappings[EncodeRequest].outputPlan.source
		if isIteration {
			parent = parent.source
		}
		output, err := encodePlannedCore(t.Context(), compiled, EncodeRequest, &Request{SchemaVersion: SemanticSchemaVersion}, EvaluationContext{})
		if err != nil || output != fragment || sharesValueStorage(parent.expression.value, output) {
			t.Fatal("a small root retained a large parent created after input measurement", isIteration, err)
		}
	}
}

func TestPlannedCoreReadKeepsAncestorThresholdAcrossBindings(t *testing.T) {
	fragment := StringValue(strings.Repeat("f", 90<<10))
	child := Value{raw: `{"fragment":` + fragment.raw + `,"padding":"` + strings.Repeat("c", 210<<10) + `"}`}
	parent := Value{raw: `{"child":` + child.raw + `,"padding":"` + strings.Repeat("p", 800<<10) + `"}`}
	program := Expression{Op: "let", Name: "parent", Source: &Expression{Op: "literal", Value: parent}, Body: &Expression{
		Op: "let", Name: "child", Source: expressionPointer(bindingTestRead("parent", "/child")), Body: expressionPointer(bindingTestRead("child", "/fragment")),
	}}
	definition := testDefinition("ancestor-ownership", "m", "h", "t", "r")
	definition.Directions[EncodeRequest] = Mapping{Transform: &program}
	compiled := compileTestDefinition(t, definition)
	output, err := encodePlannedCore(t.Context(), compiled, EncodeRequest, &Request{SchemaVersion: SemanticSchemaVersion}, EvaluationContext{})
	original := compiled.mappings[EncodeRequest].outputPlan.source.expression.value
	if err != nil || output != fragment || sharesValueStorage(original, output) {
		t.Fatal("a small root retained a larger ancestor across bindings", err)
	}
	external := requestValueReader(StringValue("small"))
	minimum := external.minimumSharedBytes
	selected, err := readOutputPointer(JSONLeaf(original), []string{"child", "fragment"}, external)
	if err != nil || external.minimumSharedBytes != minimum || selected.value != fragment || sharesValueStorage(original, selected.value) {
		t.Fatal("a temporary parent read changed an external reader", err)
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
