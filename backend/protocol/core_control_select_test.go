package protocol

import "testing"

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
