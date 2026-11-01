package protocol

import (
	"errors"
	"reflect"
	"testing"
)

func TestDeclaredMetadataSelection(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "response-metadata", Order: 220, Enabled: true, Phase: ConversionResponse, Steps: []ProtocolControlStep{{Each: &ProtocolControlEach{
		Select: "metadata", PathBinding: "location", Steps: []ProtocolControlStep{
			{When: expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/value"), literalExpression("keep")}}), Stop: true},
			{Omit: true, Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Source: expressionPointer(readExpression("input", "/path")), Reason: literalExpression("target omits this field")}},
		},
	}}}}}
	compiled := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	metadata := func(value, path string) ResponseMetadata {
		return ResponseMetadata{Codec: "openai-chat", Location: "response", Name: "service_tier", Value: StringValue(value), Path: path}
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Metadata: []ResponseMetadata{metadata("drop", "/first"), metadata("keep", "/second"), metadata("drop", "/third")}, Content: []Node{{Kind: TextNode, Payload: StringValue("text"), Metadata: []ResponseMetadata{metadata("drop", "/nested")}}}}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	sink := &DiagnosticSink{}
	result, err := conversion.Response(t.Context(), response, route, sink)
	if err != nil || len(result.Metadata) != 1 || !result.Metadata[0].Value.IsString("keep") || len(result.Content[0].Metadata) != 0 {
		t.Fatal("local stop or compaction changed traversal", result, err)
	}
	issues := sink.Issues()
	if len(issues) != 3 || issues[0].Path != "/first" || issues[1].Path != "/third" || issues[2].Path != "/nested" || issues[0].RuleID != "response-metadata" || issues[0].PolicyHash != conversion.Hash {
		t.Fatal("metadata diagnostics lost source order or policy attribution", issues)
	}
	if len(response.Metadata) != 3 || len(response.Content[0].Metadata) != 1 {
		t.Fatal("selector changed caller-owned metadata")
	}
	policy.Mode = "strict"
	conversion, err = ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversion.Response(t.Context(), response, route, nil); err == nil {
		t.Fatal("strict projection silently omitted metadata")
	}
}

func TestDeclaredUsageSelectionValidatesBeforeOmission(t *testing.T) {
	document := eventProtocolPackage(t)
	path := Expression{Op: "pointer_join", Source: expressionPointer(Expression{Op: "read", From: "binding", Name: "usage-path"}), Items: []Expression{literalExpression("details"), literalExpression("private")}}
	selection := &ProtocolControlEach{Select: "usage", PathBinding: "usage-path", Steps: []ProtocolControlStep{{
		Set:        map[string]Expression{"/details/private": {Op: "omit"}},
		Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Source: &path, Reason: literalExpression("target omits detail")},
	}}}
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "event-usage", Order: 300, Enabled: true, Phase: ConversionEvent, Steps: []ProtocolControlStep{{Each: selection}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	usage := &Usage{Details: map[string]Counter{"private": {Count: 0, Origin: ObservedCount}}}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: UsageUpdated, Usage: usage, Response: &Response{SchemaVersion: ProtocolCoreVersion, Usage: MergeUsage(nil, usage)}}
	sink := &DiagnosticSink{}
	result, err := conversion.Event(t.Context(), event, route, sink)
	if err != nil || len(result.Usage.Details) != 0 || len(result.Response.Usage.Details) != 0 {
		t.Fatal("usage selection missed an owner", result, err)
	}
	issues := sink.Issues()
	if len(issues) != 2 || issues[0].Path != "/usage/details/private" || issues[1].Path != "/response/usage/details/private" {
		t.Fatal("usage diagnostic paths changed", issues)
	}
	usage.Details["private"] = Counter{Count: -1, Origin: ObservedCount}
	if _, err := conversion.Event(t.Context(), event, route, nil); err == nil {
		t.Fatal("omission hid an invalid original counter")
	}
}

func TestDeclaredNodeSelectionKeepsOwnersAndScope(t *testing.T) {
	document := eventProtocolPackage(t)
	selection := &ProtocolControlEach{Select: "node", PathBinding: "node-path", Steps: []ProtocolControlStep{{
		When: expressionPointer(Expression{Op: "all", Items: []Expression{
			{Op: "equal", Items: []Expression{readExpression("root", "/schemaVersion"), literalExpression(ProtocolCoreVersion)}},
			{Op: "exists", Source: expressionPointer(readExpression("input", "/id"))},
		}}),
		Set: map[string]Expression{"/id": {Op: "omit"}, "/status": literalExpression("completed")},
		Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Source: expressionPointer(Expression{Op: "read", From: "binding", Name: "node-path"}),
			Reason: literalExpression("target omits the item identity")},
	}}}
	for _, phase := range []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent} {
		document.Mapping.ConversionDefaults = append(document.Mapping.ConversionDefaults, ProtocolConversionDefault{ID: string(phase) + "-nodes", Order: 230, Enabled: true, Phase: phase, Steps: []ProtocolControlStep{{Each: selection}}})
	}
	compiled := compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	node := Node{Kind: ReasoningNode, ID: StringValue("reasoning"), ReasoningForm: StructuredReasoning,
		Children:         []Node{{Kind: TextNode, ID: StringValue("summary"), Payload: StringValue("summary")}},
		ReasoningContent: []Node{{Kind: TextNode, ID: StringValue("visible"), Payload: StringValue("visible")}},
		Source:           &Provenance{Path: "/original"}, Resources: []Resource{{Kind: "signature", ID: StringValue("signed")}}}
	for _, fixture := range []struct {
		name, path string
		input      any
	}{
		{"request", "/content/0", &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}},
		{"response", "/content/0", &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}},
		{"event-item", "/item", Event{SchemaVersion: ProtocolCoreVersion, Type: ItemSnapshot, ItemID: StringValue("item"), Item: &node}},
		{"event-response", "/response/content/0", Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseFinished, Response: &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}}},
		{"event-request", "/request/content/0", Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseCreate, Request: &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			before, err := EncodeValue(fixture.input)
			if err != nil {
				t.Fatal(err)
			}
			sink := &DiagnosticSink{}
			var result any
			switch input := fixture.input.(type) {
			case *Request:
				result, err = conversion.Request(t.Context(), input, route, sink)
			case *Response:
				result, err = conversion.Response(t.Context(), input, route, sink)
			case Event:
				result, err = conversion.Event(t.Context(), input, route, sink)
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := EncodeValue(result)
			if err != nil {
				t.Fatal(err)
			}
			issues := sink.Issues()
			for index, suffix := range []string{"", "/children/0", "/reasoningContent/0"} {
				path := fixture.path + suffix
				pointer, _ := parsePointer(path)
				value, err := readValuePointer(wire, pointer)
				fields, fieldErr := value.ReadObject()
				if err != nil || fieldErr != nil || !fields["id"].IsZero() || !fields["status"].IsString("completed") {
					t.Fatal("node update missed a real owner", path, value, err, fieldErr)
				}
				if len(issues) != 3 || issues[index].Path != path || issues[index].PolicyHash != conversion.Hash {
					t.Fatal("node path or attribution changed", issues)
				}
			}
			pointer, _ := parsePointer(fixture.path)
			value, _ := readValuePointer(wire, pointer)
			var observed Node
			if err := value.Decode(&observed); err != nil || observed.Source.Path != "/original" || !observed.Resources[0].ID.IsString("signed") ||
				!observed.Children[0].Payload.IsString("summary") || !observed.ReasoningContent[0].Payload.IsString("visible") {
				t.Fatal("projection changed protected state or text", observed, err)
			}
			after, err := EncodeValue(fixture.input)
			if err != nil || before != after {
				t.Fatal("node selection mutated the caller", err)
			}
		})
	}
	policy.Mode = "strict"
	conversion, err = ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversion.Response(t.Context(), &Response{SchemaVersion: ProtocolCoreVersion, Content: []Node{node}}, route, nil); err == nil {
		t.Fatal("strict node projection discarded identity")
	}
}

func TestDeclaredNodeControlWriteBoundary(t *testing.T) {
	for _, field := range []string{"/source", "/resources", "/native", "/payload", "/children", "/kind"} {
		steps := []ProtocolControlStep{{Each: &ProtocolControlEach{Select: "node", Steps: []ProtocolControlStep{{Set: map[string]Expression{field: {Op: "omit"}}}}}}}
		if _, err := compileConversionControlSteps(steps, newExpressionCompiler(DefaultLimits(), nil), "/steps", reflect.TypeFor[Response](), false); err == nil {
			t.Fatal("node selector granted an undeclared write", field)
		}
	}
	omission := []ProtocolControlStep{{Each: &ProtocolControlEach{Select: "node", Steps: []ProtocolControlStep{{Omit: true,
		Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Reason: literalExpression("discard node")}}}}}}
	if _, err := compileConversionControlSteps(omission, newExpressionCompiler(DefaultLimits(), nil), "/steps", reflect.TypeFor[Response](), false); err == nil {
		t.Fatal("node selector permitted dropping a whole node")
	}
	for _, field := range []string{"/attributes", "/attributes/owner/secret"} {
		if _, _, err := compileResponseAttributeSetter(field); err == nil {
			t.Fatal("attribute setter accepted a whole object or nested patch", field)
		}
	}
}

func TestDeclaredResponseAttributeUpdate(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "response-attributes", Order: 230, Enabled: true, Phase: ConversionResponse,
		Steps: []ProtocolControlStep{{Set: map[string]Expression{"/attributes/created_at": {Op: "omit"}, "/attributes/a~1b": literalExpression(7)},
			Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Path: "/attributes/created_at", Reason: literalExpression("target omits creation time")}}}}}
	document.Mapping.ConversionDefaults = append(document.Mapping.ConversionDefaults, ProtocolConversionDefault{ID: "event-attributes", Order: 230, Enabled: true, Phase: ConversionEvent,
		Steps: []ProtocolControlStep{{Set: map[string]Expression{"/response/attributes/created_at": {Op: "omit"}, "/response/attributes/a~1b": literalExpression(7)},
			Diagnostic: &ProtocolControlDiagnostic{Kind: "degrade", Path: "/response/attributes/created_at", Reason: literalExpression("target omits creation time")}}}})
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	response := &Response{SchemaVersion: ProtocolCoreVersion, Attributes: Object{"created_at": StringValue("observed"), "retained": StringValue("value")}}
	result, err := conversion.Response(t.Context(), response, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
	if err != nil || !result.Attributes["created_at"].IsZero() || !result.Attributes["retained"].IsString("value") || result.Attributes["a/b"].raw != "7" || response.Attributes["created_at"].IsZero() || !response.Attributes["a/b"].IsZero() {
		t.Fatal("attribute update changed another owner or the caller", result, response, err)
	}
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, Response: response}
	projected, err := conversion.Event(t.Context(), event, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
	if err != nil || !projected.Response.Attributes["created_at"].IsZero() || !projected.Response.Attributes["retained"].IsString("value") || projected.Response.Attributes["a/b"].raw != "7" || response.Attributes["created_at"].IsZero() {
		t.Fatal("event attribute update changed another owner or the caller", projected, err)
	}
	event.Response = nil
	_, err = conversion.Event(t.Context(), event, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Path != "/response" || failure.Issues[0].Code != InvalidInput || failure.Issues[0].Direction != EncodeEvent || failure.Issues[0].Stage != "conversion.event" || failure.Issues[0].RuleID != "event-attributes" {
		t.Fatal("event attribute update invented or skipped a missing response", err)
	}
}
