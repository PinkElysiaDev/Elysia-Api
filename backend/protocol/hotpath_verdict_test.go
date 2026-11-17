package protocol

import (
	"reflect"
	"testing"
)

func hotPathToolRequest() *Request {
	return &Request{SchemaVersion: ProtocolCoreVersion, Content: []Node{
		{Kind: ToolCallNode, Name: StringValue("lookup"), CallID: StringValue("call"), Input: &ToolInput{Kind: JSONInput, Value: Value{raw: `{}`}}},
		{Kind: ToolResultNode, CallID: StringValue("call"), Payload: StringValue("original"), Status: Value{raw: `{"isError":false}`}},
	}}
}

func TestRequestVerdictRevalidatesConversionMutations(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "invalid"}[invalid], func(t *testing.T) {
			document := textProtocolPackage(t)
			set := map[string]Expression{"/payload": literalExpression("changed")}
			if invalid {
				set = map[string]Expression{"/status": literalExpression(map[string]any{"isError": nil})}
			}
			document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "mutate-content", Order: 100, Enabled: true, Phase: ConversionRequest, Steps: []ProtocolControlStep{{Each: &ProtocolControlEach{Select: "tool_result", Steps: []ProtocolControlStep{{Set: set}}}}}}}
			compiled := compilePackageForTest(t, document)
			conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			request := hotPathToolRequest()
			target := Target{Protocol: compiled.Identity(), Direction: EncodeRequest, Capabilities: CapabilitySet{FunctionToolsCapability: true}}
			if err := validateRequestContext(request); err != nil {
				t.Fatal(err)
			}
			if issues := CheckRequest(request, target, DefaultLimits()); len(issues) != 0 {
				t.Fatal(issues)
			}
			retainRequestVerdict(request, target, DefaultLimits())
			if !requestContextUnchanged(request) || !requestCheckUnchanged(request, target, DefaultLimits()) {
				t.Fatal("valid witness did not match")
			}
			converted, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
			if invalid {
				assertPackageRuntimeIssue(t, err, InvalidInput, "/content/1/status/isError")
			} else if err != nil || converted.verdict != nil || !converted.Content[1].Payload.IsString("changed") {
				t.Fatal("content edit retained a stale pass or lost the edit", converted, err)
			}
			if !request.Content[1].Payload.IsString("original") || !requestContextUnchanged(request) {
				t.Fatal("conversion edited the source witness")
			}
		})
	}
}

func TestRequestVerdictRejectsPublicEditsAndDifferentInputs(t *testing.T) {
	request := hotPathToolRequest()
	target := Target{Direction: EncodeRequest, Capabilities: CapabilitySet{FunctionToolsCapability: true}}
	retainRequestVerdict(request, target, DefaultLimits())
	request.Content[1].Status = Value{raw: `{"isError":null}`}
	if requestContextUnchanged(request) {
		t.Fatal("nested public mutation reused a pass")
	}
	assertPackageRuntimeIssue(t, validateRequestContext(request), InvalidInput, "/content/1/status/isError")
	request.Content[1].Status = Value{raw: `{"isError":false}`}
	request.Content[0].Kind = NodeKind("invalid")
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) == 0 || issues[0].Path != "/content/0/kind" {
		t.Fatal("invalid content reused a pass", issues)
	}
	request.Content[0].Kind = ToolCallNode
	target.Capabilities[FunctionToolsCapability] = false
	if issues := CheckRequest(request, target, DefaultLimits()); len(issues) == 0 || issues[0].Code != UnsupportedCapability {
		t.Fatal("changed capabilities reused a pass", issues)
	}
	target.Capabilities[FunctionToolsCapability] = true
	limits := DefaultLimits()
	limits.Nodes = 1
	if issues := CheckRequest(request, target, limits); len(issues) == 0 || issues[0].Code != LimitExceeded {
		t.Fatal("changed limits reused a pass", issues)
	}
	changed := target
	changed.Scope.Account = "other"
	if requestCheckUnchanged(request, changed, DefaultLimits()) {
		t.Fatal("changed scope reused a pass")
	}
}

func TestRequestReplayPreservesBaselineWarnings(t *testing.T) {
	document := noCachePackage(t)
	document.Mapping.Native.Preserve = true
	entry := document.Mapping.Directions[EncodeRequest]
	currentWarning := packageWarning()
	currentWarning.ID = "current-model-advisory"
	currentWarning.When = expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("caller")}})
	entry.Effects = append(entry.Effects, packageWarning(), currentWarning)
	document.Mapping.Directions[EncodeRequest] = entry
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	replay := newRequestReplay(compiled, EvaluationContext{})
	replay.current = request
	if _, err := replay.load(t.Context(), request.Native); err != nil {
		t.Fatal(err)
	}
	if replay.canReuseOutput(t.Context()) {
		t.Fatal("warning-bearing mapping skipped its independent baseline execution")
	}
	// Identical warnings are intentionally collapsed by DiagnosticSink. Make
	// the current and original model differ so both distinct observations must
	// survive, in current-then-baseline order, with every issue field intact.
	request.Model = StringValue("caller")
	sink := &DiagnosticSink{}
	if _, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{Diagnostics: sink}); err != nil {
		t.Fatal(err)
	}
	want := make([]ConversionIssue, 0, 2)
	for _, id := range []string{currentWarning.ID, "model-advisory"} {
		for _, effect := range compiled.packagePlan.effects[EncodeRequest] {
			if effect.id == id {
				issue := compiled.packageEffectIssue(EncodeRequest, effect, effect.reason)
				issue.Code, issue.Stage, issue.Suggestion = effect.diagnostic.Code, effect.diagnostic.Stage, effect.diagnostic.Suggestion
				issue.Path, issue.Severity, issue.Fidelity = effect.path, SeverityWarning, "preserved"
				want = append(want, issue)
			}
		}
	}
	if len(want) != 2 || !reflect.DeepEqual(sink.Issues(), want) {
		t.Fatal("baseline warning multiplicity/order changed", sink.Issues(), want)
	}
}

func TestRequestVerdictDoesNotCertifyScopeResolverEdits(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Capabilities[FunctionToolsCapability] = true
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{ResolveRequestScope: func(request *Request) (Scope, error) {
		request.Content = hotPathToolRequest().Content
		request.Content[1].Status = Value{raw: `{"isError":null}`}
		return Scope{}, nil
	}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if request.verdict != nil {
		t.Fatal("resolver edits acquired the earlier context-validation pass")
	}
	_, err = compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, InvalidInput, "/content/1/status/isError")
}

func TestRequestReplayReusesOnlyProvenDecodeAndOutput(t *testing.T) {
	document := noCachePackage(t)
	document.Mapping.Native.Preserve = true
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{Values: Object{"binding": StringValue("original")}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	workspace := request.Native.requestWorkspace
	if workspace.finished == nil || workspace.finished == request {
		t.Fatal("missing privately owned finished decode")
	}
	replay := newRequestReplay(compiled, options)
	replay.current = request
	prepared, err := replay.load(t.Context(), request.Native)
	if err != nil || prepared.baseline != workspace.finished || !replay.canReuseOutput(t.Context()) {
		t.Fatal("unchanged decode/output did not reuse immutable evidence", err)
	}
	request.ClientOutput = &ClientOutput{CollectUsage: true}
	if !requestContextUnchanged(request) || replay.canReuseOutput(t.Context()) {
		t.Fatal("delivery-only changes must preserve content validation, NOT mapping output reuse")
	}
	request.ClientOutput = workspace.finished.ClientOutput
	request.Content[0].Children[0].Payload = StringValue("edited")
	if replay.canReuseOutput(t.Context()) {
		t.Fatal("edited input reused baseline output")
	}
	if !workspace.finished.Content[0].Children[0].Payload.IsString("hello") {
		t.Fatal("caller owned the immutable baseline")
	}
	changedOptions := options
	changedOptions.Values = Object{"binding": StringValue("changed")}
	replay = newRequestReplay(compiled, changedOptions)
	replay.current = cloneRequestSemantic(workspace.finished)
	if _, err = replay.load(t.Context(), request.Native); err != nil || replay.canReuseOutput(t.Context()) {
		t.Fatal("different encode context reused output", err)
	}
	options.ResolveRequestScope = func(request *Request) (Scope, error) { request.Model = StringValue("authorized"); return Scope{}, nil }
	authorized, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), options)
	if err != nil || authorized.Native.requestWorkspace.finished != nil {
		t.Fatal("authorization edits became an original baseline", err)
	}
	baseline, err := compiled.prepareRequestReplay(t.Context(), authorized.Native, options)
	if err != nil || !baseline.baseline.Model.IsString("m") {
		t.Fatal("source baseline included authorization edits", err)
	}
	if !reflect.DeepEqual(request.Native.Source, workspace.source) {
		t.Fatal("replay edited provenance")
	}
}
