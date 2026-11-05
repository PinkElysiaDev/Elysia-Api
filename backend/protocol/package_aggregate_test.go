package protocol

import (
	"reflect"
	"strings"
	"testing"
)

// This package is an isolated declarative example, not an installed preset.
func cacheOwnerPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := aggregateOwnerPackage(t, "cache", "cache_control", &ProtocolAggregateLayout{Remainder: "value", Fields: map[string]string{"ttl": "ttl"}, Labels: Object{"kind": StringValue("breakpoint"), "location": StringValue("request")}})
	document.Mapping.Capabilities[CacheBreakpointsCapability] = true
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		document.Mapping.Directions[direction].Effects[2].ID = "cache-owner"
	}
	return document
}

func aggregateOwnerPackage(t *testing.T, owner, wire string, layout *ProtocolAggregateLayout) ProtocolPackage {
	t.Helper()
	document := verifiedTextPackage(t, "object-owner")
	document.Mapping.Native.Preserve = true
	for _, direction := range []Direction{DecodeResponse, EncodeResponse} {
		mapping := document.Mapping.Directions[direction]
		mapping.Capabilities = CapabilitySet{TextCapability: true}
		document.Mapping.Directions[direction] = mapping
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		contract := document.Spec.Documents[document.Mapping.Directions[direction].Document]
		contract.Schema.AllowUnknown = true
		contract.Schema.Properties[wire] = ValueSchema{Type: ObjectType, Nullable: true, AllowUnknown: true}
		document.Spec.Documents[document.Mapping.Directions[direction].Document] = contract
	}
	input := "/" + escapePointer(wire)
	core := "/" + escapePointer(owner) + "/0"
	read := func(path string) *Expression { return expressionPointer(readExpression("input", path)) }
	exists := func(path string) *Expression { return &Expression{Op: "exists", Source: read(path)} }
	labels := func(value Expression) map[string]Expression {
		fields := map[string]Expression{layout.Remainder: value}
		for name, label := range layout.Labels {
			fields[name] = Expression{Op: "literal", Value: label}
		}
		return fields
	}
	parts := labels(Expression{Op: "project", Source: read(input), Keys: sortedKeys(layout.Fields), Policy: "exclude"})
	reconstruction := map[string]Expression{}
	for wire, member := range layout.Fields {
		parts[member] = *read(input + "/" + escapePointer(wire))
		reconstruction[wire] = *read(core + "/" + escapePointer(member))
	}
	decode := document.Mapping.Directions[DecodeRequest]
	decode.Program.Fields[owner] = Expression{Op: "if", When: exists(input), Then: &Expression{Op: "array", Items: []Expression{
		{Op: "if", When: &Expression{Op: "present", Source: read(input)}, Then: expressionPointer(objectExpression(parts)), Otherwise: expressionPointer(objectExpression(labels(*read(input))))},
	}}}
	decode.Effects = append(decode.Effects, ProtocolSemanticEffect{ID: "object-owner", Kind: "aggregate", Path: core, Target: input, When: exists(input), Layout: layout})
	document.Mapping.Directions[DecodeRequest] = decode
	encode := document.Mapping.Directions[EncodeRequest]
	remainder := core + "/" + escapePointer(layout.Remainder)
	encode.Program.Fields[wire] = Expression{Op: "if", When: exists(core), Then: &Expression{
		Op: "if", When: &Expression{Op: "equal", Items: []Expression{*read(remainder), literalExpression(nil)}}, Then: read(remainder),
		Otherwise: &Expression{Op: "merge", Policy: "reject", Items: []Expression{*read(remainder), objectExpression(reconstruction)}},
	}}
	encode.Effects = append(encode.Effects, ProtocolSemanticEffect{ID: "object-owner", Kind: "aggregate", Path: core, Target: input, When: exists(core), Layout: layout})
	document.Mapping.Directions[EncodeRequest] = encode
	return document
}

func TestPackageAggregateCacheOwner(t *testing.T) {
	document := cacheOwnerPackage(t)
	compiled := compilePackageForTest(t, document)
	for _, test := range []struct {
		name, policy, value, ttl string
	}{
		{"missing", "", "", ""},
		{"null", `null`, `null`, ""},
		{"no-ttl", `{"type":"ephemeral"}`, `{"type":"ephemeral"}`, ""},
		{"null-ttl", `{"type":"ephemeral","ttl":null}`, `{"type":"ephemeral"}`, `null`},
		{"unknown", `{"type":"ephemeral","ttl":"1h","future":{"n":900719925474099312345,"spelling":1e+03}}`, `{"future":{"n":900719925474099312345,"spelling":1e+03},"type":"ephemeral"}`, `"1h"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"model":"m","prompt":"hello","future_root":true`
			if test.policy != "" {
				raw += `,"cache_control":` + test.policy
			}
			raw += `}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(raw), EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			var expected []CacheIntent
			if test.policy != "" {
				expected = []CacheIntent{{Kind: "breakpoint", Location: "request", Value: Value{raw: test.value}, TTL: Value{raw: test.ttl}}}
			}
			if !reflect.DeepEqual(request.Cache, expected) {
				t.Fatal("cache data changed", request.Cache, expected)
			}
			output, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
			if err != nil || output.raw != raw {
				t.Fatal("native cache representation changed", output.raw, err)
			}
		})
	}
	// Existing offline verification observes the feature through a real sample.
	wire := Value{raw: `{"model":"m","prompt":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}`}
	var expected Request
	if err := decodeContract(document.Mapping.Samples[1].Input.Bytes(), &expected); err != nil {
		t.Fatal(err)
	}
	expected.Cache = []CacheIntent{{Kind: "breakpoint", Location: "request", Value: Value{raw: `{"type":"ephemeral"}`}, TTL: StringValue("1h")}}
	core := fixtureValue(t, expected)
	document.Mapping.Samples = append(document.Mapping.Samples,
		Sample{ID: "cache.decode", Direction: DecodeRequest, Input: wire, Expected: core, Capabilities: []Capability{CacheBreakpointsCapability}},
		Sample{ID: "cache.encode", Direction: EncodeRequest, Input: core, Expected: wire, Capabilities: []Capability{CacheBreakpointsCapability}})
	report := Verify(t.Context(), compilePackageForTest(t, document))
	if !report.Passed {
		t.Fatal("cache package lacks executable offline evidence", report)
	}
}

func TestPackageAggregateCacheEdits(t *testing.T) {
	compiled := compilePackageForTest(t, cacheOwnerPackage(t))
	const original = `{"model":"m","prompt":"hello","cache_control":{"type":"ephemeral","ttl":"1h","future":900719925474099312345}}`
	request, err := compiled.DecodeRequest(t.Context(), []byte(original), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Cache[0].TTL = Value{}
	request.Cache[0].Location = "custom-location"
	if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{}); err == nil {
		t.Fatal("aggregate accepted a different declared location")
	}
	request.Cache[0].Location = "request"
	output, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal("cache reconstruction failed", err)
	}
	policy, err := readValuePointer(output, []string{"cache_control"})
	if err != nil || policy.raw != `{"future":900719925474099312345,"type":"ephemeral"}` {
		t.Fatal("deleted TTL revived or extension changed", policy.raw, err)
	}
	request.Cache = append(request.Cache, request.Cache[0])
	if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{}); err == nil {
		t.Fatal("multiple intents silently used the first owner")
	}
	request.Cache = request.Cache[:1]
	for _, intent := range []CacheIntent{
		{Kind: "key", Location: "request", Value: StringValue("key")},
		{Kind: "breakpoint", Value: Value{raw: "null"}, TTL: StringValue("1h")},
		{Kind: "breakpoint", Value: Value{raw: `{"type":"ephemeral","ttl":"1h"}`}},
		{Kind: "breakpoint", Value: Value{raw: `{"type":"ephemeral"}`}, Resource: &Resource{Kind: "cache", ID: StringValue("r")}},
	} {
		request.Cache[0] = intent
		if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{}); err == nil {
			t.Fatal("invalid core cache accepted by typed API", intent)
		}
		core := fixtureValue(t, request)
		if _, issues := compiled.Execute(t.Context(), EncodeRequest, core, EvaluationContext{}); IssuesError(issues) == nil {
			t.Fatal("raw execution discarded invalid aggregate data", intent)
		}
	}
	request.Cache[0] = CacheIntent{Kind: "breakpoint", Location: "request", Value: Value{raw: `{"type":"ephemeral"}`}, TTL: StringValue("1h")}
	core := fixtureValue(t, request)
	aliased, err := ApplyMutations(core, []Mutation{{Op: DeleteValue, Path: "/cache/0/ttl"}, {Op: SetValue, Path: "/cache/0/TTL", Value: StringValue("1h")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, issues := compiled.Execute(t.Context(), EncodeRequest, aliased, EvaluationContext{}); IssuesError(issues) == nil {
		t.Fatal("raw execution validated a folded field then dropped its value")
	}
}

func TestPackageAggregateRejectsUnprovedLayouts(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"missing-layout", func(p *ProtocolPackage) { p.Mapping.Directions[DecodeRequest].Effects[2].Layout = nil }},
		{"extra-label", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Effects[2].Layout.Labels["role"] = StringValue("assistant")
		}},
		{"foreign-owner", func(p *ProtocolPackage) { p.Mapping.Directions[DecodeRequest].Effects[2].Path = "/tools/0/cache/0" }},
		{"protected-owner", func(p *ProtocolPackage) { p.Mapping.Directions[DecodeRequest].Effects[2].Path = "/native/source/scope" }},
		{"protected-resource", func(p *ProtocolPackage) {
			effect := &p.Mapping.Directions[DecodeRequest].Effects[2]
			effect.Path = "/cache/0/resource"
			effect.Layout = &ProtocolAggregateLayout{Remainder: "id", Labels: Object{"kind": StringValue("cache")}}
		}},
		{"duplicate-member", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Effects[2].Layout.Fields["future"] = "ttl"
		}},
		{"wrong-label-type", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Effects[2].Layout.Labels["kind"] = Value{raw: "1"}
		}},
		{"no-guard", func(p *ProtocolPackage) { p.Mapping.Directions[DecodeRequest].Effects[2].When = nil }},
		{"lossy-project", func(p *ProtocolPackage) {
			fields := p.Mapping.Directions[DecodeRequest].Program.Fields["cache"].Then.Items[0].Then.Fields
			value := fields["value"]
			value.Keys = []string{"ttl", "future"}
			fields["value"] = value
		}},
		{"changed-label", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Program.Fields["cache"].Then.Items[0].Then.Fields["kind"] = literalExpression("key")
		}},
		{"lost-null", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Program.Fields["cache"].Then.Items[0].Otherwise.Fields["value"] = literalExpression(nil)
		}},
		{"changed-ttl", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeRequest].Program.Fields["cache"].Then.Items[0].Then.Fields["ttl"] = literalExpression("5m")
		}},
		{"duplicate-owner", func(p *ProtocolPackage) {
			array := p.Mapping.Directions[DecodeRequest].Program.Fields["cache"].Then
			array.Items = append(array.Items, array.Items[0])
		}},
		{"merge-overwrite", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeRequest].Program.Fields["cache_control"].Then.Otherwise.Policy = "last"
		}},
		{"changed-reconstruction", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeRequest].Program.Fields["cache_control"].Then.Otherwise.Items[1].Fields["ttl"] = literalExpression("5m")
		}},
		{"conditional-owner", func(p *ProtocolPackage) {
			mapping := p.Mapping.Directions[EncodeRequest]
			mapping.Program = Expression{Op: "if", When: expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("m")}}), Then: expressionPointer(mapping.Program), Otherwise: expressionPointer(objectExpression(nil))}
			p.Mapping.Directions[EncodeRequest] = mapping
		}},
		{"overlapping-deep-merge", func(p *ProtocolPackage) {
			mapping := p.Mapping.Directions[EncodeRequest]
			mapping.Program = Expression{Op: "merge_deep", Items: []Expression{mapping.Program, objectExpression(map[string]Expression{"cache_control": literalExpression(map[string]string{"future": "value"})})}}
			p.Mapping.Directions[EncodeRequest] = mapping
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := cacheOwnerPackage(t)
			test.change(&document)
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			value := fixtureValue(t, document)
			if compiled, issues := compiler.CompilePackage(value.Bytes()); compiled != nil || IssuesError(issues) == nil {
				t.Fatal("unproved aggregate compiled", issues)
			}
		})
	}
	// A declared layout is part of the package identity, not an ignored annotation.
	document := cacheOwnerPackage(t)
	before := compilePackageForTest(t, document)
	copy, _ := before.Package()
	copy.Mapping.Directions[DecodeRequest].Effects[2].Reason = "object partition evidence"
	after := compilePackageForTest(t, copy)
	if before.Hash() == after.Hash() || before.mappings[DecodeRequest].definitionHash == after.mappings[DecodeRequest].definitionHash {
		t.Fatal("aggregate declaration changes retained old evidence identity")
	}
	if strings.Contains(string(fixtureValue(t, document).Bytes()), "module") {
		t.Fatal("cache fixture accidentally selected an installed codec")
	}
}

func TestPackageAggregateToolDefinition(t *testing.T) {
	document := aggregateOwnerPackage(t, "tools", "tool_definition", &ProtocolAggregateLayout{Remainder: "inputSchema", Fields: map[string]string{"name": "name"}, Labels: Object{"kind": StringValue("function")}})
	document.Mapping.Capabilities[FunctionToolsCapability] = true
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello","tool_definition":{"name":"lookup","type":"object","future":1}}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Kind != FunctionTool || !request.Tools[0].Name.IsString("lookup") || !equalValues(request.Tools[0].InputSchema, Value{raw: `{"type":"object","future":1}`}) {
		t.Fatal("declared tool partition lost fields", request.Tools)
	}
	request.Native = nil
	request.Tools[0].Name = StringValue("updated")
	output, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := readValuePointer(output, []string{"tool_definition"})
	if err != nil || !equalValues(definition, Value{raw: `{"name":"updated","type":"object","future":1}`}) {
		t.Fatal("declared tool reconstruction lost its edit or remainder", output, err)
	}
	request.Tools[0].Description = StringValue("unmapped")
	if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{}); err == nil {
		t.Fatal("aggregate discarded an unrepresented core member")
	}
}
