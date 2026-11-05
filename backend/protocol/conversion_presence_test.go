package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestConversionPresenceMatchesEncodedCore(t *testing.T) {
	node := Node{Kind: ToolResultNode, Payload: Value{raw: ` {"a/b":null,"number":9007199254740993} `}, Children: []Node{}, ReasoningContent: []Node{}}
	for _, document := range []any{
		&Request{SchemaVersion: 1, Model: StringValue("model"), Content: []Node{node}, Parameters: Object{"a/b": Value{raw: "null"}}, Tools: []Tool{}},
		&Response{SchemaVersion: 1, Content: []Node{node}},
		&Event{SchemaVersion: 1, Type: ItemStarted, Item: &node}, &node,
	} {
		wire, err := EncodeValue(document)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"", "/model", "/model/child", "/tools", "/parameters/a~1b", "/content/0/payload/a~1b", "/content/00", "/item/payload", "/payload/a~1b", "/children", "/reasoningContent"} {
			for _, present := range []*bool{nil, boolPtr(false), boolPtr(true)} {
				for _, source := range []string{"", "other"} {
					match := ConversionMatch{Path: path, Present: present, SourceFamily: source}
					if got, want := match.matchesSemantic(ConversionContext{}, document), match.matches(ConversionContext{}, wire); got != want {
						t.Fatalf("%T %s: typed=%v wire=%v", document, path, got, want)
					}
				}
			}
		}
	}
}

func TestConversionPresenceKeepsRawWireAndErrorOrder(t *testing.T) {
	input := Value{raw: `{"schemaVersion":1,"content":[],"future":true}`}
	for _, present := range []bool{false, true} {
		match := ConversionMatch{Path: "/future", Present: boolPtr(present)}
		state := conversionState{wire: input}
		if got := state.matches(match, ConversionContext{}); got != present {
			t.Fatal("unloaded state ignored the authoritative wire", got)
		}
		if err := state.validateContext(ConversionRequest); err != nil {
			t.Fatal(err)
		}
		if got := state.matches(match, ConversionContext{}); got != present {
			t.Fatal("validation-only decoding hid an unknown field", got)
		}
		conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "wire-presence", Rules: []ConversionRule{{ID: "stream", Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic"), Match: match}}})
		if err != nil {
			t.Fatal(err)
		}
		output, err := conversion.ApplyValue(t.Context(), ConversionRequest, input, ConversionContext{}, nil)
		if present {
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatal("matched rule skipped strict contract decoding", err)
			}
		} else if err != nil || output != input {
			t.Fatal("unmatched rule changed the wire or triggered strict decoding", output, err)
		}
	}
	for _, phase := range []ConversionPhase{ConversionResponse, ConversionEvent} {
		for _, present := range []bool{false, true} {
			document := eventProtocolPackage(t)
			document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "core-fields", Order: 1, Enabled: true, Phase: phase,
				Match: ConversionMatch{Path: "/future", Present: boolPtr(present)}, Steps: []ProtocolControlStep{{Set: map[string]Expression{"/usage": {Op: "omit"}}}}}}
			compiled := compilePackageForTest(t, document)
			conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			raw := Value{raw: `{"schemaVersion":2,"future":true}`}
			output, err := conversion.ApplyValue(t.Context(), phase, raw, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
			if present {
				if err == nil || !strings.Contains(err.Error(), "unknown field") {
					t.Fatal("core projection silently removed an unknown field", phase, err)
				}
			} else if err != nil || output != raw {
				t.Fatal("unmatched core projection changed its raw boundary", phase, output, err)
			}
		}
	}
}

func TestOwnedPresenceConversionBoundaries(t *testing.T) {
	request := &Request{SchemaVersion: 1, Model: StringValue("model"), Parameters: Object{"stream": Value{raw: "false"}}, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("lookup"), Options: Object{"eager_input_streaming": Value{raw: "true"}}}}, Content: []Node{{Kind: ToolResultNode, Payload: Value{raw: `{"result":"hello <world>"}`}}}}
	for _, rule := range []ConversionRule{
		{Action: "stream_options", Match: ConversionMatch{Path: "/tools/0/options/eager_input_streaming", Present: boolPtr(true)}},
		{Action: "stream_options", Match: ConversionMatch{Path: "/model/child", Present: boolPtr(false)}},
		{Action: "stream_options", Match: ConversionMatch{Path: "/ignored"}},
		{Action: "tool_result_text", Match: ConversionMatch{NodeKind: ToolResultNode, Path: "/payload", Present: boolPtr(true)}},
	} {
		rule.ID, rule.Phase, rule.Enabled, rule.Value = "presence", ConversionRequest, true, StringValue("anthropic")
		for _, mode := range []string{"compatible", "strict"} {
			conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "presence-boundary", Mode: mode, Rules: []ConversionRule{rule}})
			if err != nil || !conversion.canOwnRequest() {
				t.Fatal("typed presence lost its owned entrypoint", err)
			}
			wire, err := EncodeSemantic(t.Context(), DecodeRequest, request)
			if err != nil {
				t.Fatal(err)
			}
			before, after := &DiagnosticSink{}, &DiagnosticSink{}
			expected, expectedErr := conversion.ApplyValue(t.Context(), ConversionRequest, wire, ConversionContext{}, before)
			actual, actualErr := conversion.Request(t.Context(), request, ConversionContext{}, after)
			if fmt.Sprint(actualErr) != fmt.Sprint(expectedErr) || !reflect.DeepEqual(before.Issues(), after.Issues()) {
				t.Fatal("presence changed diagnostics", rule, actualErr, expectedErr, after.Issues(), before.Issues())
			}
			if actualErr == nil {
				output := fixtureValue(t, actual)
				if output != expected {
					t.Fatal("presence changed normalized output", output, expected)
				}
				if rule.Action == "tool_result_text" && !actual.Content[0].Payload.IsStringValue() {
					t.Fatal("node presence was evaluated against the request root")
				}
			}
		}
	}
	if request.Tools[0].Options["eager_input_streaming"].raw != "true" || !request.Content[0].Payload.IsObject() {
		t.Fatal("candidate conversion mutated the caller")
	}
}

func TestConversionRequestClientOutputSettings(t *testing.T) {
	rawOptions := Value{raw: `{"include_usage":false}`}
	for _, fixture := range []struct {
		name     string
		settings *ConversionUsageSettings
		input    *ClientOutput
		want     *ClientOutput
	}{
		{
			name:     "default-usage",
			settings: &ConversionUsageSettings{DefaultIncludeUsage: true, CollectUpstreamUsage: true},
			want:     &ClientOutput{IncludeUsage: boolPtr(true), CollectUsage: true},
		},
		{
			name:     "explicit-exclusion",
			settings: &ConversionUsageSettings{DefaultIncludeUsage: true, CollectUpstreamUsage: true},
			input:    &ClientOutput{IncludeUsage: boolPtr(false), RawStreamOptions: rawOptions},
			want:     &ClientOutput{IncludeUsage: boolPtr(false), RawStreamOptions: rawOptions, CollectUsage: true},
		},
		{
			name:     "collection-disabled",
			settings: &ConversionUsageSettings{},
			input:    &ClientOutput{IncludeUsage: boolPtr(true), CollectUsage: true},
			want:     &ClientOutput{IncludeUsage: boolPtr(true)},
		},
		{
			name:  "settings-absent",
			input: &ClientOutput{RawStreamOptions: Value{raw: "null"}, CollectUsage: true},
			want:  &ClientOutput{RawStreamOptions: Value{raw: "null"}, CollectUsage: true},
		},
		{name: "settings-and-output-absent"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "client-output-settings", Usage: fixture.settings, Rules: []ConversionRule{}})
			if err != nil {
				t.Fatal(err)
			}
			for _, typed := range []bool{false, true} {
				name := "raw"
				if typed {
					name = "typed"
				}
				t.Run(name, func(t *testing.T) {
					candidate := *conversion
					candidate.ownedPhases = map[ConversionPhase]bool{ConversionRequest: typed}
					request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("model"), ClientOutput: fixture.input}
					before := fixtureValue(t, request)
					result, err := candidate.Request(t.Context(), request, ConversionContext{}, nil)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(result.ClientOutput, fixture.want) {
						t.Fatalf("client output = %+v, want %+v", result.ClientOutput, fixture.want)
					}
					if fixtureValue(t, request) != before {
						t.Fatal("request settings mutated the caller")
					}
					if result.ClientOutput != nil {
						result.ClientOutput.CollectUsage = !result.ClientOutput.CollectUsage
						if result.ClientOutput.IncludeUsage != nil {
							*result.ClientOutput.IncludeUsage = !*result.ClientOutput.IncludeUsage
						}
					}
					if fixtureValue(t, request) != before {
						t.Fatal("request settings share mutable caller state")
					}
				})
			}
		})
	}
}

func BenchmarkConversionPresenceBoundary(b *testing.B) {
	request := &Request{SchemaVersion: 1, Parameters: Object{"stream": Value{raw: "false"}}, Content: []Node{{Kind: ImageNode, Payload: StringValue(strings.Repeat("A", 2<<20))}}}
	conversion, err := CompileConversion(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "presence-benchmark", Rules: []ConversionRule{{ID: "stream", Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic"), Match: ConversionMatch{Path: "/parameters/stream", Present: boolPtr(true)}}}})
	if err != nil {
		b.Fatal(err)
	}
	for _, hasOwnedEntry := range []bool{false, true} {
		candidate := *conversion
		candidate.ownedPhases = map[ConversionPhase]bool{ConversionRequest: hasOwnedEntry}
		name := "json-entry"
		if hasOwnedEntry {
			name = "typed-presence"
		}
		if _, err := candidate.Request(b.Context(), request, ConversionContext{}, nil); err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				output, err := candidate.Request(b.Context(), request, ConversionContext{}, nil)
				if err != nil || output.Content[0].Payload != request.Content[0].Payload {
					b.Fatal("presence changed payload", err)
				}
			}
		})
	}
}
