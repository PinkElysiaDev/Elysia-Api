package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestPackageTypedDecodeRejectsAuthoredOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		expression  Expression
	}{
		{"native-literal", "native", literalExpression(map[string]any{"source": map[string]any{"path": "/not-in-wire"}, "value": "invented"})},
		{"source-literal", "source", literalExpression(map[string]any{"path": "/not-in-wire"})},
		{"native-wire", "native", readExpression("input", "/spoof")},
		{"native-deep-rebuilt", "native", Expression{Op: "merge_deep", Items: []Expression{
			{Op: "capture_native", Source: expressionPointer(readExpression("input", "/prompt"))}, objectExpression(nil),
		}}},
		{"native-rebuilt", "native", Expression{Op: "merge", Policy: "last", Items: []Expression{
			{Op: "capture_native", Source: expressionPointer(readExpression("input", "/prompt"))},
			objectExpression(map[string]Expression{"value": literalExpression("invented")}),
		}}},
		{"native-json-roundtrip", "native", Expression{Op: "parse_json", Source: &Expression{Op: "stringify_json", Source: &Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", "/prompt"))}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			document.Mapping.Native.Preserve = true
			document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.AllowUnknown = true
			child := document.Mapping.Directions[DecodeRequest].Program.Fields["content"].Items[0].Fields["children"].Items[0]
			child.Fields[tc.field] = tc.expression
			origin := Provenance{Protocol: document.Spec.Identity(), Direction: DecodeRequest, Path: "/prompt"}
			if tc.name == "source-literal" {
				child.Fields[tc.field] = literalExpression(origin)
			} else if tc.name == "native-literal" {
				child.Fields[tc.field] = literalExpression(Native{Source: origin, Value: StringValue("hello")})
			}
			compiled := compilePackageForTest(t, document)
			wire := Value{raw: `{"model":"m","prompt":"hello","spoof":{"source":{"path":"/prompt"},"value":"invented"}}`}
			// Raw Execute is an unstamped authoring result. Serialization cannot
			// turn that result into trusted provenance for the typed request API.
			if _, issues := compiled.Execute(t.Context(), DecodeRequest, wire, EvaluationContext{}); IssuesError(issues) != nil {
				t.Fatal("inspection boundary changed", issues)
			}
			calls := 0
			request, err := compiled.DecodeRequestValue(t.Context(), wire, EvaluationContext{ResolveRequestScope: func(*Request) (Scope, error) { calls++; return Scope{}, nil }})
			var originError *untrustedCoreOriginError
			if request != nil || !errors.As(err, &originError) || calls != 0 {
				t.Fatal("authored provenance reached stamping or authorization", request, err, calls)
			}
			if strings.Contains(err.Error(), "invented") || strings.Contains(err.Error(), "not-in-wire") {
				t.Fatal("origin rejection exposed authored data", err)
			}
		})
	}
}

func TestPackageOriginCheckCoversRawRootsAndNestedContainers(t *testing.T) {
	for _, size := range []int{0, 128 << 10} {
		for _, hasRawRoot := range []bool{true, false} {
			document := textProtocolPackage(t)
			document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.AllowUnknown = true
			mapping := document.Mapping.Directions[DecodeRequest]
			mapping.Effects = mapping.Effects[:1]
			if hasRawRoot {
				mapping.Program = readExpression("input", "/core")
				mapping.Effects[0].Target = "/core/model"
			} else {
				mapping.Program.Fields["content"] = readExpression("input", "/core/content")
			}
			document.Mapping.Directions[DecodeRequest] = mapping
			compiled := compilePackageForTest(t, document)
			wire := []byte(`{"model":"m","prompt":"hello","core":{"schemaVersion":2,"model":"m","content":[{"kind":"text","payload":"` + strings.Repeat("x", size) + `","native":{"source":{},"value":"invented"}}]}}`)
			_, err := compiled.DecodeRequest(t.Context(), wire, EvaluationContext{})
			var originError *untrustedCoreOriginError
			if !errors.As(err, &originError) {
				t.Fatal("raw contract shortcut bypassed origin checks", size, hasRawRoot, err)
			}
		}
	}
}

func TestPackageCaptureTagsSurviveReadsButNotPayloadInspection(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	child := document.Mapping.Directions[DecodeRequest].Program.Fields["content"].Items[0].Fields["children"].Items[0]
	capture := Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", "/prompt"))}
	child.Fields["native"] = Expression{Op: "let", Name: "snapshot", Source: &capture, Body: expressionPointer(bindingTestRead("snapshot", ""))}
	child.Fields["source"] = Expression{Op: "let", Name: "snapshot", Source: &capture, Body: expressionPointer(bindingTestRead("snapshot", "/source"))}
	userData := literalExpression(map[string]any{"native": map[string]any{"source": map[string]any{"path": "/ordinary-data"}, "value": "payload"}, "source": map[string]any{"scope": "payload"}})
	document.Mapping.Directions[DecodeRequest].Program.Fields["parameters"] = objectExpression(map[string]Expression{"native": userData, "source": userData})
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"<"}`), EvaluationContext{Scope: Scope{Provider: "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	node := request.Content[0].Children[0]
	if node.Native == nil || node.Source == nil || node.Native.Source.Path != "/prompt" || node.Source.Scope.Provider != "provider" || !node.Native.Value.IsString("<") || request.Parameters["native"].IsZero() {
		t.Fatal("capture or opaque user JSON was rejected", request)
	}
	document.Mapping.Native.Preserve = false
	withoutNative := compilePackageForTest(t, document)
	request, err = withoutNative.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
	if err != nil || request.Content[0].Children[0].Native != nil || request.Content[0].Children[0].Source != nil {
		t.Fatal("disabled capture retained an origin", err)
	}
}

func TestLegacyTypedMappingKeepsItsOriginContract(t *testing.T) {
	document := testDefinition("legacy-origins", "m", "h", "t", "r")
	request := literalExpression(map[string]any{"content": []any{map[string]any{"kind": "text", "payload": "hello", "native": map[string]any{"source": map[string]any{"path": "/old"}, "value": "legacy"}}}})
	document.Directions[DecodeRequest] = Mapping{Transform: &request}
	compiled := compileTestDefinition(t, document)
	output, err := compiled.DecodeRequest(t.Context(), []byte(`{}`), EvaluationContext{})
	if err != nil || output.Content[0].Native == nil || output.Content[0].Native.Source.Path != "/old" {
		t.Fatal("v3 guard changed the legacy mapping contract", err)
	}
}
