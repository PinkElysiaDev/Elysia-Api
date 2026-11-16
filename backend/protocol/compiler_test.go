package protocol

import (
	"context"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func literalExpression(value any) Expression {
	encoded, _ := EncodeValue(value)
	return Expression{Op: "literal", Value: encoded}
}
func readExpression(from, path string) Expression {
	return Expression{Op: "read", From: from, Path: path}
}
func expressionPointer(value Expression) *Expression { return &value }
func objectExpression(fields map[string]Expression) Expression {
	return Expression{Op: "object", Fields: fields}
}
func arrayMap(source Expression, body Expression) Expression {
	return Expression{Op: "map", Source: &source, Body: &body}
}

func optionalArrayMap(source Expression, body Expression) Expression {
	return Expression{Op: "if", When: &Expression{Op: "exists", Source: &source}, Then: expressionPointer(arrayMap(source, body))}
}

func testDefinition(id, model, history, functions, reply string) ProtocolPackage {
	decodeNode := objectExpression(map[string]Expression{"kind": readExpression("item", "/kind"), "role": readExpression("item", "/speaker"), "callId": readExpression("item", "/call"), "name": readExpression("item", "/operation"), "input": readExpression("item", "/input"), "payload": readExpression("item", "/result"), "children": readExpression("item", "/parts")})
	encodeNode := objectExpression(map[string]Expression{"kind": readExpression("item", "/kind"), "speaker": readExpression("item", "/role"), "call": readExpression("item", "/callId"), "operation": readExpression("item", "/name"), "input": readExpression("item", "/input"), "result": readExpression("item", "/payload"), "parts": readExpression("item", "/children")})
	decodeTool := objectExpression(map[string]Expression{"kind": literalExpression("function"), "name": readExpression("item", "/operation"), "inputSchema": readExpression("item", "/schema")})
	encodeTool := objectExpression(map[string]Expression{"operation": readExpression("item", "/name"), "schema": readExpression("item", "/inputSchema")})
	decodeRequest := objectExpression(map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "model": readExpression("input", "/"+model), "content": arrayMap(readExpression("input", "/"+history), decodeNode), "tools": optionalArrayMap(readExpression("input", "/"+functions), decodeTool)})
	encodeRequest := objectExpression(map[string]Expression{model: readExpression("input", "/model"), history: arrayMap(readExpression("input", "/content"), encodeNode), functions: optionalArrayMap(readExpression("input", "/tools"), encodeTool)})
	decodeResponse := objectExpression(map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "id": readExpression("input", "/request"), "content": arrayMap(readExpression("input", "/"+reply), decodeNode), "status": literalExpression("completed")})
	encodeResponse := objectExpression(map[string]Expression{"request": readExpression("input", "/id"), reply: arrayMap(readExpression("input", "/content"), encodeNode)})
	decodeEvent := objectExpression(map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion), "type": Expression{Op: "enum", Source: expressionPointer(readExpression("input", "/event")), Values: map[string]Value{"token": StringValue(string(ItemDelta)), "end": StringValue(string(ResponseFinished))}}, "delta": readExpression("input", "/token"), "itemId": readExpression("input", "/item")})
	encodeEvent := objectExpression(map[string]Expression{"event": Expression{Op: "enum", Source: expressionPointer(readExpression("input", "/type")), Values: map[string]Value{string(ItemDelta): StringValue("token"), string(ResponseFinished): StringValue("end")}}, "token": readExpression("input", "/delta"), "item": readExpression("input", "/itemId")})
	return ProtocolPackage{
        SchemaVersion: ProtocolPackageSchemaVersion,
        Spec: ProtocolSpec{ID: id, Name: id, Version: "1", Family: id, WireVersion: "1",
            Documents: map[string]WireContractSpec{"test": {Schema: &ValueSchema{Type: AnyType}}, "event": {Schema: &ValueSchema{Type: AnyType}, Stream: testFrameContract()}},
            Operations: map[string]ProtocolOperationSpec{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: HTTPJSON, Request: "test", Response: "test", Auth: Credential{Location: "none"}}}},
        Mapping: ProtocolMapping{CoreVersion: ProtocolCoreVersion, DSLVersion: ProtocolDSLVersion,
            Capabilities: CapabilitySet{TextCapability: true, FunctionToolsCapability: true},
            Directions: map[Direction]ProtocolDirectionMapping{
                DecodeRequest: {Document: "test", Program: decodeRequest, Effects: []ProtocolSemanticEffect{{ID: "model", Kind: "emit", Path: "/model", Target: "/"+model}}}, EncodeRequest: {Document: "test", Program: encodeRequest, Effects: []ProtocolSemanticEffect{{ID: "model", Kind: "emit", Path: "/model", Target: "/"+model}}},
                DecodeResponse: {Document: "test", Program: decodeResponse, Effects: []ProtocolSemanticEffect{{ID: "id", Kind: "emit", Path: "/id", Target: "/request", When: expressionPointer(Expression{Op: "present", Source: expressionPointer(readExpression("input", "/request"))})}}}, EncodeResponse: {Document: "test", Program: encodeResponse, Effects: []ProtocolSemanticEffect{{ID: "id", Kind: "emit", Path: "/id", Target: "/request", When: expressionPointer(Expression{Op: "present", Source: expressionPointer(readExpression("input", "/id"))})}}},
                DecodeEvent: {Document: "event", Program: decodeEvent}, EncodeEvent: {Document: "event", Program: encodeEvent}},
            Operations: map[string]ProtocolOperationMapping{"generate": {Request: EncodeRequest, Response: DecodeResponse}},
            Samples: []Sample{}},
    }
}

func compileTestDefinition(t *testing.T, definition ProtocolPackage) *Compiled {
	t.Helper()
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := sonic.ConfigStd.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(encoded)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestCompilerHTTPStreamIdleRange(t *testing.T) {
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []Transport{SSE, NDJSON, HTTPJSON} {
		for _, idle := range []int{-1, 0, 1, 120000, 600000, 900000, 3600000, 3600001} {
			definition := testDefinition("idle-range", "m", "h", "t", "r")
			operation := definition.Spec.Operations["generate"]
			operation.Transport = transport
			if transport != HTTPJSON {
				operation.Response = "event"
				definition.Mapping.Operations["generate"] = ProtocolOperationMapping{Request: EncodeRequest, Response: DecodeEvent}
			}
			operation.Framing = &Framing{IdleMillis: idle}
			definition.Spec.Operations["generate"] = operation
			body, err := sonic.ConfigStd.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(body)
			wantValid := idle == 0 || (transport != HTTPJSON && idle > 0 && idle <= 3600000)
			if (compiled != nil && IssuesError(issues) == nil) != wantValid {
				t.Fatalf("transport %s idle %d: want valid %v, got %v", transport, idle, wantValid, issues)
			}
		}
	}
}

func TestCompilerNewProtocolsConvertAllDirectionsWithoutModules(t *testing.T) {
	first := compileTestDefinition(t, testDefinition("from-scratch-alpha", "engine", "history", "functions", "answer"))
	second := compileTestDefinition(t, testDefinition("from-scratch-beta", "modelId", "turns", "operations", "output"))
	input := []byte(`{"engine":"model","history":[{"kind":"message","speaker":"system","parts":[{"kind":"text","payload":"rules"}]},{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}]},{"kind":"tool_call","call":"c1","operation":"lookup","input":{"kind":"json","value":{"n":9007199254740993}}},{"kind":"tool_result","call":"c1","result":"ok"}],"functions":[{"operation":"lookup","schema":{"type":"object"}}]}`)
	request, err := first.DecodeRequest(t.Context(), input, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	converted, err := second.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(converted), `"modelId":"model"`) || !strings.Contains(string(converted), `9007199254740993`) {
		t.Fatalf("converted: %s", converted)
	}
	decoded, err := second.DecodeRequest(t.Context(), converted, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := first.EncodeRequest(t.Context(), decoded, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ParseValue(input)
	got, _ := ParseValue(roundtrip)
	if !equalValues(want, got) {
		t.Fatalf("request roundtrip: %s", roundtrip)
	}
	response, err := second.DecodeResponse(t.Context(), []byte(`{"request":"r","output":[{"kind":"message","speaker":"assistant","parts":[{"kind":"text","payload":"answer"}]}]}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := first.EncodeResponse(t.Context(), response, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"answer":[`) {
		t.Fatalf("response: %s", result)
	}
	frame, _ := ParseValue([]byte(`{"event":"token","item":"i","token":"text"}`))
	events, err := first.DecodeEvents(t.Context(), frame, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != ItemDelta {
		t.Fatalf("events: %+v", events)
	}
	encoded, err := second.EncodeEvent(t.Context(), events[0], EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !equalValues(encoded, frame) {
		t.Fatalf("event conversion: %s", encoded.Bytes())
	}
}

func TestCompilerPreservesScalarPresenceAndExactNumbers(t *testing.T) {
	definition := testDefinition("presence", "m", "h", "t", "r")
	definition.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: objectExpression(map[string]Expression{"absent": readExpression("input", "/missing"), "null": readExpression("input", "/null"), "false": readExpression("input", "/false"), "zero": readExpression("input", "/zero"), "empty": readExpression("input", "/empty"), "large": readExpression("input", "/large")})}
	compiled := compileTestDefinition(t, definition)
	input, _ := ParseValue([]byte(`{"null":null,"false":false,"zero":0,"empty":[],"large":9007199254740993123}`))
	output, err := evaluateFixturePlan(t.Context(), compiled, EncodeRequest, input)
	if err != nil {
		t.Fatal(err)
	}
	if !equalValues(input, output) {
		t.Fatalf("presence: %s", output.Bytes())
	}
}

func TestCompilerRejectsUnknownAndInvalidDefinitions(t *testing.T) {
	base := testDefinition("invalid", "m", "h", "t", "r")
	raw, _ := sonic.ConfigStd.Marshal(base)
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	for name, body := range map[string][]byte{
		"unknown root":         []byte(strings.TrimSuffix(string(raw), "}") + `,"ignoredCriticalOption":true}`),
		"trailing JSON":        append(append([]byte{}, raw...), []byte(` {}`)...),
		"unknown nested":       []byte(strings.Replace(string(raw), `"op":"read"`, `"op":"read","typo":false`, 1)),
		"irrelevant empty key": []byte(strings.Replace(string(raw), `"op":"literal"`, `"op":"literal","from":""`, 1)),
		"unsupported schema":   []byte(strings.Replace(string(raw), `"schemaVersion":4`, `"schemaVersion":7`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if compiled, issues := compiler.Compile(body); compiled != nil || IssuesError(issues) == nil {
				t.Fatalf("accepted invalid definition: %+v", issues)
			}
		})
	}
	for name, edit := range map[string]func(*ProtocolPackage){
		"unknown program": func(d *ProtocolPackage) {
			d.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: Expression{Op: "not-installed"}}
		},
		"item outside loop": func(d *ProtocolPackage) {
			d.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: readExpression("item", "/x")}
		},
		"wrong type": func(d *ProtocolPackage) {
			d.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: arrayMap(literalExpression(true), readExpression("item", ""))}
		},
		"recursive ref": func(d *ProtocolPackage) {
			d.Mapping.Expressions = map[string]Expression{"self": {Op: "ref", Ref: "self"}}
			d.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: Expression{Op: "ref", Ref: "self"}}
		},
		"unreachable ref": func(d *ProtocolPackage) {
			d.Mapping.Expressions = map[string]Expression{"unused": literalExpression(1)}
		},
		"unsupported transport": func(d *ProtocolPackage) {
			op := d.Spec.Operations["generate"]
			op.Transport = WebSocket
			d.Spec.Operations["generate"] = op
		},
		"absolute URL": func(d *ProtocolPackage) {
			op := d.Spec.Operations["generate"]
			op.Path = "https://example.org/steal"
			d.Spec.Operations["generate"] = op
		},
		"unimplemented capability": func(d *ProtocolPackage) { d.Mapping.Capabilities["telepathy"] = true },
		"increased limits": func(d *ProtocolPackage) {
			limits := DefaultLimits()
			limits.Nodes++
			d.Mapping.Limits = &limits
		},
	} {
		t.Run(name, func(t *testing.T) {
			definition := testDefinition("invalid", "m", "h", "t", "r")
			edit(&definition)
			body, _ := sonic.ConfigStd.Marshal(definition)
			if compiled, issues := compiler.Compile(body); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("invalid definition compiled")
			}
		})
	}
}

func TestCompiledDefinitionIsImmutableAndChecksCancellation(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("immutable", "m", "h", "t", "r"))
	copy, _ := compiled.Package()
	delete(copy.Mapping.Directions, EncodeRequest)
	if !compiled.Supports(EncodeRequest) {
		t.Fatal("copy changed compiled revision")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	input, _ := ParseValue([]byte(`{"m":"model","h":[],"t":[]}`))
	_, issues := compiled.Execute(ctx, DecodeRequest, input, EvaluationContext{})
	if IssuesError(issues) == nil {
		t.Fatal("cancelled mapping executed")
	}
}

func TestCompilerRequiresAllDeclaredDiscriminatorBranches(t *testing.T) {
	definition := testDefinition("branches", "m", "h", "t", "r")
	definition.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeEvent), Document: testDocument(DecodeEvent), Program: Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/type")), Cases: map[string]Expression{"text": literalExpression("text")}}}
	definition.Spec.Documents["event"] = WireContractSpec{Schema: &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"type": {Type: StringType, Enum: []Value{StringValue("text"), StringValue("tool")}}}}, Stream: testFrameContract()}
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	body, _ := sonic.ConfigStd.Marshal(definition)
	_, issues := compiler.Compile(body)
	if len(issues) == 0 || issues[0].Path != "/mapping/directions/decode_event/program" {
		t.Fatalf("missing branch was not located: %+v", issues)
	}
}

func TestRuntimeLocatesInvalidInputExpression(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("location", "m", "h", "t", "r"))
	input, _ := ParseValue([]byte(`{"m":"model","h":false}`))
	_, issues := compiled.Execute(t.Context(), DecodeRequest, input, EvaluationContext{})
	if len(issues) != 1 || issues[0].Code != InvalidInput || issues[0].Path != "/mapping/directions/decode_request/program/fields/content" {
		t.Fatalf("wrong expression diagnostic: %+v", issues)
	}
}
