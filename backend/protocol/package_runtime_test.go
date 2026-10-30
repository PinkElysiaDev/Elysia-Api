package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPackageRuntimeRoundTrip(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	options := EvaluationContext{Scope: Scope{Provider: "local", Account: "synthetic", Model: "m"}}
	requestWire := []byte(`{"model":"m","prompt":"hello <world>"}`)
	request, err := compiled.DecodeRequest(t.Context(), requestWire, options)
	if err != nil {
		t.Fatal(err)
	}
	if request.SchemaVersion != ProtocolCoreVersion || request.Source != compiled.Identity() || compiled.CoreVersion() != ProtocolCoreVersion {
		t.Fatal("package request lost its pinned core contract", request)
	}
	encodedRequest, err := compiled.EncodeRequest(t.Context(), request, options)
	if err != nil || !equalValues(Value{raw: string(requestWire)}, Value{raw: string(encodedRequest)}) {
		t.Fatal("typed request round trip changed", err, string(encodedRequest))
	}
	responseWire := []byte(`{"reply":"answer"}`)
	response, err := compiled.DecodeResponse(t.Context(), responseWire, options)
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != ProtocolCoreVersion || response.Source != compiled.Identity() {
		t.Fatal("package response lost its pinned core contract", response)
	}
	encodedResponse, err := compiled.EncodeResponse(t.Context(), response, options)
	if err != nil || !equalValues(Value{raw: string(responseWire)}, Value{raw: string(encodedResponse)}) {
		t.Fatal("typed response round trip changed", err, string(encodedResponse))
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
		mapping := compiled.mappings[direction]
		if mapping.module != nil || (!mapping.hasCoreInputPlan && !mapping.hasCoreOutputPlan) {
			t.Fatal("package runtime selected an intermediate semantic JSON boundary", direction)
		}
	}
	if issues := CheckRequest(request, compiled.target(EncodeRequest, options), DefaultLimits()); len(issues) != 0 {
		t.Fatal("pinned request check rejected core2", issues)
	}
	if issues := CheckResponse(response, bindingTarget(compiled, Binding{Capabilities: compiled.Capabilities(EncodeResponse)}, EncodeResponse, options.Scope), DefaultLimits()); len(issues) != 0 {
		t.Fatal("binding target lost its core version", issues)
	}
	if issues := CheckRequest(request, Target{Capabilities: compiled.Capabilities(EncodeRequest)}, DefaultLimits()); len(issues) != 1 || issues[0].Path != "/schemaVersion" {
		t.Fatal("an unbound legacy target accepted core2", issues)
	}
	request.SchemaVersion, response.SchemaVersion = SemanticSchemaVersion, SemanticSchemaVersion
	_, err = compiled.EncodeRequest(t.Context(), request, options)
	assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion")
	_, err = compiled.EncodeResponse(t.Context(), response, options)
	assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion")
}

func TestPackageRawRequestReasoningValidation(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	core, issues := compiled.Execute(t.Context(), DecodeRequest, Value{raw: `{"model":"m","prompt":"hello"}`}, EvaluationContext{})
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fields string
	}{
		{"absent", ""},
		{"null", `,"reasoning":null`},
		{"valid", `,"reasoning":{"mode":"enabled","budget":1024,"future":true}`},
		{"without-tools", `,"reasoning":{"mode":"enabled","budget":1024,"interleaved":true}`},
		{"invalid-scope", `,"reasoning":{"effort":"high","effortScope":"unknown"}`},
		{"invalid-interleaved", `,"reasoning":{"mode":"disabled","interleaved":true}`},
		{"invalid-string", `,"reasoning":"disabled"`},
		{"invalid-array", `,"reasoning":[]`},
		{"invalid-boolean", `,"reasoning":false`},
		{"invalid-number", `,"reasoning":1`},
		{"unrelated-hints", `,"parameters":{"user":1},"future":true`},
		{"case-alias", `,"Reasoning":{"effort":"high","effortScope":"unknown"}`},
		{"duplicate-merge", `,"reasoning":{"budget":1024},"reasoning":{"mode":"disabled"}`},
		{"duplicate-type-error", `,"reasoning":1,"reasoning":null`},
		{"duplicate-null-reset", `,"reasoning":{"effort":"high","effortScope":"unknown"},"reasoning":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := Value{raw: strings.TrimSuffix(core.raw, "}") + tc.fields + "}"}
			var request Request
			wantErr := input.Decode(&request)
			if wantErr == nil {
				wantErr = ValidateReasoningIntent(request.Reasoning)
			}
			_, issues := compiled.Execute(t.Context(), EncodeRequest, input, EvaluationContext{})
			gotErr := IssuesError(issues)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("raw reasoning acceptance changed: got %v, want %v", gotErr, wantErr)
			}
			var original *ConversionError
			if errors.As(wantErr, &original) && (len(issues) != 1 || issues[0].Code != original.Issues[0].Code || issues[0].Path != original.Issues[0].Path) {
				t.Fatal("raw reasoning diagnostic changed", issues, original.Issues)
			}
		})
	}
}

func TestPackageDecodeRootReadIsolation(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	for _, prompt := range []string{"first", "second", "third", "fourth"} {
		t.Run(prompt, func(t *testing.T) {
			t.Parallel()
			request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"`+prompt+`"}`), EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if request.Content[0].Children[0].Payload != StringValue(prompt) {
				t.Fatal("concurrent decode reused another request's root")
			}
		})
	}
}

func TestPackageRuntimeRetainsWireAndEffectChecks(t *testing.T) {
	document := textProtocolPackage(t)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
		path := "/prompt"
		if direction == DecodeResponse || direction == EncodeResponse {
			path = "/reply"
		}
		contract := document.Spec.Documents[document.Mapping.Directions[direction].Document]
		contract.Assertions = []WireAssertion{{Path: path, Schema: ValueSchema{Type: StringType, Enum: []Value{StringValue("allowed")}}, Code: ConversionRejected, Reason: "synthetic wire rule"}}
		document.Spec.Documents[document.Mapping.Directions[direction].Document] = contract
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		entry := document.Mapping.Directions[direction]
		condition := Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("blocked")}}
		entry.Effects = append([]ProtocolSemanticEffect{{ID: "model-guard", Kind: "reject", Path: "/model", Target: "/model", When: &condition, Reason: "synthetic model rejection"}}, entry.Effects...)
		document.Mapping.Directions[direction] = entry
	}
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{}
	_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"private"}`), options)
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/prompt")
	_, err = compiled.DecodeResponse(t.Context(), []byte(`{"reply":"private"}`), options)
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/reply")
	_, err = compiled.DecodeRequest(t.Context(), []byte(`{"model":"blocked","prompt":"allowed"}`), options)
	if issue := assertPackageRuntimeIssue(t, err, ConversionRejected, "/model"); issue.RuleID != "model-guard" {
		t.Fatal("decode ignored its reject effect", issue)
	}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"allowed"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	request.Model = StringValue("blocked")
	_, err = compiled.EncodeRequest(t.Context(), request, options)
	if issue := assertPackageRuntimeIssue(t, err, ConversionRejected, "/model"); issue.RuleID != "model-guard" {
		t.Fatal("typed input ignored its reject effect", issue)
	}
	request.Model = Value{}
	_, err = compiled.EncodeRequest(t.Context(), request, options)
	if issue := assertPackageRuntimeIssue(t, err, ConversionRejected, "/model"); issue.RuleID != "model" {
		t.Fatal("typed input ignored its emit presence check", issue)
	}
	request.Model = StringValue("m")
	request.Content[0].Children[0].Payload = StringValue("private")
	_, err = compiled.EncodeRequest(t.Context(), request, options)
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/prompt")
	response, err := compiled.DecodeResponse(t.Context(), []byte(`{"reply":"allowed"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	response.Content[0].Children[0].Payload = StringValue("private")
	_, err = compiled.EncodeResponse(t.Context(), response, options)
	assertPackageRuntimeIssue(t, err, ConversionRejected, "/reply")
}

func TestPackageRuntimeDoesNotReplaceCoreVersion(t *testing.T) {
	for _, version := range []Expression{literalExpression(1), literalExpression(nil), {Op: "omit"}, literalExpression("2")} {
		document := textProtocolPackage(t)
		for _, direction := range []Direction{DecodeRequest, DecodeResponse} {
			document.Mapping.Directions[direction].Program.Fields["schemaVersion"] = version
		}
		compiled := compilePackageForTest(t, document)
		_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
		if issue := assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion"); issue.Stage != "mapping.core" {
			t.Fatal("request version was silently stamped", issue)
		}
		_, err = compiled.DecodeResponse(t.Context(), []byte(`{"reply":"answer"}`), EvaluationContext{})
		assertPackageRuntimeIssue(t, err, InvalidInput, "/schemaVersion")
	}
}

func TestPackageRuntimeKeepsSemanticChecks(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Capabilities[FunctionToolsCapability] = true
	document.Mapping.Capabilities[UsageCapability] = true
	compiled := compilePackageForTest(t, document)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Content = append(request.Content, Node{Kind: ToolResultNode, CallID: StringValue("absent-call"), Payload: StringValue("result")})
	_, err = compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, InvalidAssociation, "/content/1/callId")
	response, err := compiled.DecodeResponse(t.Context(), []byte(`{"reply":"answer"}`), EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	response.Usage = &Usage{Input: &Counter{Count: -1, Origin: ObservedCount}}
	_, err = compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, InvalidInput, "/usage/input")

	document.Mapping.Capabilities = CapabilitySet{}
	withoutText := compilePackageForTest(t, document)
	_, err = withoutText.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
	assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/content/0/children/0")
	_, err = withoutText.DecodeResponse(t.Context(), []byte(`{"reply":"answer"}`), EvaluationContext{})
	assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/content/0/children/0")
}

func TestPackageRuntimeNativePreservation(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	for name, contract := range document.Spec.Documents {
		schema := *contract.Schema
		schema.AllowUnknown = true
		contract.Schema = &schema
		document.Spec.Documents[name] = contract
	}
	compiled := compilePackageForTest(t, document)
	wire := []byte(`{"model":"m","prompt":"hello","extension":{"n":9007199254740993,"rows":[{"id":"a"},{"id":"b"}]}}`)
	request, err := compiled.DecodeRequest(t.Context(), wire, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil || string(unchanged) != string(wire) {
		t.Fatal("native replay did not retain wire bytes", err, string(unchanged))
	}
	request.Model = StringValue("changed")
	changed, err := compiled.EncodeRequest(t.Context(), request, EvaluationContext{})
	if err != nil || !strings.Contains(string(changed), `"model":"changed"`) || !strings.Contains(string(changed), `9007199254740993`) || !strings.Contains(string(changed), `"rows":[{"id":"a"},{"id":"b"}]`) {
		t.Fatal("native reconciliation lost the model edit or unknown values", err, string(changed))
	}
	responseWire := []byte(`{"reply":"answer","extension":9007199254740993}`)
	response, err := compiled.DecodeResponse(t.Context(), responseWire, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	response.Content[0].Children[0].Payload = StringValue("changed")
	changedResponse, err := compiled.EncodeResponse(t.Context(), response, EvaluationContext{})
	if err != nil || !strings.Contains(string(changedResponse), `"reply":"changed"`) || !strings.Contains(string(changedResponse), `9007199254740993`) {
		t.Fatal("native response reconciliation lost unknown values", err, string(changedResponse))
	}
	contract := document.Spec.Documents[document.Mapping.Directions[EncodeRequest].Document]
	contract.Schema.AllowUnknown = false
	document.Spec.Documents[document.Mapping.Directions[EncodeRequest].Document] = contract
	strict := compilePackageForTest(t, document)
	request, err = strict.DecodeRequest(t.Context(), wire, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = strict.EncodeRequest(t.Context(), request, EvaluationContext{})
	assertPackageRuntimeIssue(t, err, ConversionRejected, "")
}

func TestPackageRuntimeCancellationAndResourceErrors(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	input := Value{raw: `{"model":"m","prompt":"hello"}`}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := compiled.DecodeRequestValue(ctx, input, EvaluationContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("package decode lost cancellation", err)
	}
	for _, limits := range []Limits{{Depth: 1, Nodes: 1000, BufferBytes: 1000}, {Depth: 64, Nodes: 3, BufferBytes: 1000}, {Depth: 64, Nodes: 1000, BufferBytes: 10}} {
		compiled.limits, compiled.explicitLimits = limits, true
		_, wantErr := compiled.executeMapping(t.Context(), DecodeRequest, input, EvaluationContext{})
		_, gotErr := decodePlannedCore[Request](t.Context(), compiled, DecodeRequest, input, EvaluationContext{})
		var before, after *ResourceLimitError
		if !errors.As(wantErr, &before) || !errors.As(gotErr, &after) || !reflect.DeepEqual(before, after) {
			t.Fatalf("package output plan changed resource errors: got %v, want %v", gotErr, wantErr)
		}
	}
}

func assertPackageRuntimeIssue(t *testing.T, err error, code IssueCode, path string) ConversionIssue {
	t.Helper()
	var failure *ConversionError
	if !errors.As(err, &failure) {
		t.Fatal("expected conversion error", err)
	}
	for _, issue := range failure.Issues {
		if issue.Code == code && issue.Path == path {
			return issue
		}
	}
	t.Fatalf("expected %s at %s, got %+v", code, path, failure.Issues)
	return ConversionIssue{}
}
