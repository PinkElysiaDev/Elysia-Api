package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func rejectDiagnosticPackage(t *testing.T, diagnostic *RejectDiagnostic) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeRequest]
	entry.Effects = append([]ProtocolSemanticEffect{{ID: "model-guard", Kind: "reject", Path: "/model", Reason: "synthetic rejection", Diagnostic: diagnostic,
		When: expressionPointer(readExpression("input", "/parameters/reject"))}}, entry.Effects...)
	document.Mapping.Directions[EncodeRequest] = entry
	return document
}

func TestRejectDiagnosticOnlyClassifiesMatchedRejections(t *testing.T) {
	diagnostic := RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire", Suggestion: "select a compatible target"}
	legacy := compilePackageForTest(t, rejectDiagnosticPackage(t, nil))
	declared := compilePackageForTest(t, rejectDiagnosticPackage(t, &diagnostic))
	if legacy.Hash() == declared.Hash() {
		t.Fatal("diagnostic changed without invalidating compilation evidence")
	}
	for _, fixture := range []struct {
		name, condition string
		omitModel       bool
	}{
		{"accepted", "false", false},
		{"rejected", "true", false},
		{"condition-type", `"not boolean"`, false},
		{"missing-emit", "false", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Parameters: Object{"reject": parseTestValue(t, fixture.condition)},
				Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}}
			if fixture.omitModel {
				request.Model = Value{}
			}
			want, wantErr := legacy.EncodeRequestValue(t.Context(), &request, EvaluationContext{})
			got, gotErr := declared.EncodeRequestValue(t.Context(), &request, EvaluationContext{})
			if fixture.name == "accepted" {
				if wantErr != nil || gotErr != nil || want != got {
					t.Fatal("diagnostic changed successful conversion", wantErr, gotErr)
				}
				return
			}
			var before, after *ConversionError
			if !errors.As(wantErr, &before) || !errors.As(gotErr, &after) || len(before.Issues) != 1 || len(after.Issues) != 1 {
				t.Fatal("expected structured failures", wantErr, gotErr)
			}
			issue := after.Issues[0]
			if fixture.name == "rejected" {
				if issue.Code != diagnostic.Code || issue.Stage != diagnostic.Stage || issue.Suggestion != diagnostic.Suggestion || issue.Path != "/model" || issue.Reason != "synthetic rejection" || issue.RuleID != "model-guard" || issue.Protocol != declared.Identity() || issue.Direction != EncodeRequest || issue.Evidence != "protocol-mapping" || issue.Fidelity != "rejected" {
					t.Fatal("declared rejection lost attribution", issue)
				}
				issue.Code, issue.Stage, issue.Suggestion = before.Issues[0].Code, before.Issues[0].Stage, before.Issues[0].Suggestion
			} else if gotErr.Error() != wantErr.Error() || errors.Unwrap(gotErr) == nil {
				t.Fatal("diagnostic changed an unrelated error or lost its cause", wantErr, gotErr)
			}
			if !reflect.DeepEqual(issue, before.Issues[0]) {
				t.Fatal("diagnostic changed other effect fields", before.Issues[0], issue)
			}
			_, issues := declared.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
			if len(issues) != 1 || !reflect.DeepEqual(issues[0], after.Issues[0]) {
				t.Fatal("raw and typed diagnostic attribution differ", issues, after.Issues)
			}
		})
	}
}

func TestRejectDiagnosticDoesNotReplaceAggregateFailure(t *testing.T) {
	document := cacheOwnerPackage(t)
	entry := document.Mapping.Directions[EncodeRequest]
	guard := ProtocolSemanticEffect{ID: "disabled-guard", Kind: "reject", Path: "/model", When: expressionPointer(literalExpression(false)), Reason: "inactive rejection"}
	entry.Effects = append([]ProtocolSemanticEffect{guard}, entry.Effects...)
	document.Mapping.Directions[EncodeRequest] = entry
	before := compilePackageForTest(t, document)
	entry.Effects[0].Diagnostic = &RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire"}
	document.Mapping.Directions[EncodeRequest] = entry
	after := compilePackageForTest(t, document)
	input := fixtureValue(t, Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}},
		Cache: []CacheIntent{{Kind: "breakpoint", Location: "request", Value: fixtureValue(t, Object{})}, {Kind: "breakpoint", Location: "request", Value: fixtureValue(t, Object{})}}})
	_, want := before.Execute(t.Context(), EncodeRequest, input, EvaluationContext{})
	_, got := after.Execute(t.Context(), EncodeRequest, input, EvaluationContext{})
	if len(want) != 1 || want[0].RuleID != "cache-owner" || !reflect.DeepEqual(want, got) {
		t.Fatal("inactive rejection changed aggregate failure", want, got)
	}
}

func TestRejectDiagnosticCompilation(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolSemanticEffect)
	}{
		{"emit", func(effect *ProtocolSemanticEffect) { effect.Kind = "emit" }},
		{"aggregate", func(effect *ProtocolSemanticEffect) { effect.Kind = "aggregate" }},
		{"projection-scope", func(effect *ProtocolSemanticEffect) { effect.Scope = requestFieldScope }},
		{"resource-code", func(effect *ProtocolSemanticEffect) { effect.Diagnostic.Code = LimitExceeded }},
		{"unrelated-stage", func(effect *ProtocolSemanticEffect) { effect.Diagnostic.Stage = "routing" }},
		{"direction-override", func(effect *ProtocolSemanticEffect) { effect.Diagnostic.Direction = DecodeEvent }},
		{"path-override", func(effect *ProtocolSemanticEffect) { effect.Diagnostic.Path = "/other" }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := rejectDiagnosticPackage(t, &RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire"})
			fixture.edit(&document.Mapping.Directions[EncodeRequest].Effects[0])
			compiler, err := NewCompiler(DefaultLimits(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
			if compiled != nil || len(issues) != 1 || !strings.Contains(issues[0].Reason, "diagnostic") {
				t.Fatal("invalid rejection diagnostic compiled", issues)
			}
		})
	}
}
