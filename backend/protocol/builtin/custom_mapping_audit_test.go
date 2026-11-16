package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicSystemMessageCustomModulesAndExplicitHoist(t *testing.T) {
	const body = `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"system","content":"middle"}]}`
	base := testCompiled(t, Anthropic)
	document, _ := base.Package()
	document.Spec.ID = "old-anthropic-system-copy"
	response := document.Mapping.Directions[p.DecodeResponse]
	response.Program.When = &p.Expression{Op: "equal", Items: []p.Expression{*response.Program.When, {Op: "literal", Value: testValue(t, `true`)}}}
	document.Mapping.Directions[p.DecodeResponse] = response
	custom, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*p.Compiled{{base, custom}, {custom, base}, {custom, custom}} {
		from, originalTarget := pair[0], pair[1]
		for _, shouldHoist := range []bool{false, true} {
			target := originalTarget
			if shouldHoist {
				// An explicit layout override changes the target's declared core selector.
				// The source decoder and the external request contract remain independent.
				document, _ := originalTarget.Package()
				document.Mapping.Expressions["layout-system-node"] = *document.Mapping.Expressions["layout-system-node"].Otherwise
				document.Mapping.Expressions["is-top-level-system"] = *document.Mapping.Expressions["is-top-level-system"].When
				var issues []p.ConversionIssue
				target, issues = compilePackageDocument(t, document)
				if err := p.IssuesError(issues); err != nil {
					t.Fatal(err)
				}
			}
			request, err := from.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(from, target))
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: target.Identity()}, sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if err := target.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
				t.Fatal(err)
			}
			if shouldHoist {
				sameJSON(t, wire, `{"model":"m","max_tokens":64,"system":[{"type":"text","text":"middle"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
				if len(sink.Issues()) != 1 || sink.Issues()[0].Fidelity != "lossy_compatible" || sink.Issues()[0].RuleID != "system-instruction-hoist" {
					t.Fatal("explicit scope change was not diagnosed", sink.Issues())
				}
			} else {
				sameJSON(t, wire, body)
				if len(sink.Issues()) != 0 {
					t.Fatal("custom identity changed native instruction scope", sink.Issues())
				}
			}
		}
	}
}

func TestAnthropicSystemMessageCombinationAndAfterMapping(t *testing.T) {
	const body = `{"model":"m","max_tokens":64,"system":"top","messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"middle","cache_control":{"type":"ephemeral"}}],"output_config":{"effort":"medium"}}]}`
	compiled := testCompiled(t, Anthropic)
	document, _ := compiled.Package()
	document.Spec.ID = "system-message-evidence"
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{
		ID: "native-system-message", Direction: p.DecodeRequest, Input: testValue(t, body), Capabilities: []p.Capability{p.TextCapability},
		Expected: testValue(t, `{"schemaVersion":2,"model":"m","parameters":{"max_output_tokens":64},"clientOutput":{"responsesStorage":{"sourceCodec":"anthropic","requested":false,"effective":false}},"content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"top"}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"hi"}]},{"kind":"message","role":"system","attributes":{"wire:claude":{"output_config":{"effort":"medium"}}},"children":[{"kind":"text","payload":"middle","cache":[{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"}}]}]}]}`),
	})
	withSample, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), withSample); !report.Passed {
		t.Fatal(report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), withSample, compiled, packageMapping(compiled).Capabilities)
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	hasSystemEvidence := false
	for _, check := range report.Checks {
		hasSystemEvidence = hasSystemEvidence || check.SampleID == "native-system-message" && check.Passed && !check.Skipped
	}
	if !hasSystemEvidence {
		t.Fatal("system message sample omitted from binding evidence")
	}
	request, err := withSample.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	document.Mapping.Native.Preserve = false
	mapping := document.Mapping.Directions[p.EncodeRequest]
	original := mapping.Program
	mapping.Program = p.Expression{Op: "let", Name: "encoded-request", Source: &original, Body: &p.Expression{Op: "literal", Value: testValue(t, `{"model":"m","max_tokens":64,"messages":[{"role":"developer","content":"bad role"}]}`)}}
	document.Mapping.Directions[p.EncodeRequest] = mapping
	invalid, issues := compilePackageDocument(t, document)
	if invalid != nil || len(issues) == 0 || issues[0].Code != p.InvalidDefinition || !strings.Contains(issues[0].Path, "/effects") {
		t.Fatal("destructive authored mapping bypassed its semantic effect proof", issues)
	}
	if err := compiled.ValidateWireOutput(p.EncodeRequest, mapping.Program.Body.Value); err == nil {
		t.Fatal("authored developer role bypassed the independent final request contract")
	}
	if len(request.Content) != 3 || !request.Content[2].Role.IsString("system") {
		t.Fatal("authored output test changed the decoded instruction input")
	}
}

func TestResponsesMetadataCustomMappingsAndLimits(t *testing.T) {
	const frameByteLimit = 4096
	from := testCompiled(t, Responses)
	frame := decodeResponsesMetadataFixture(t, from, p.Scope{})
	document, _ := from.Package()
	document.Spec.ID = "metadata-responses-copy"
	custom, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	out, err := custom.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(out) != 1 || out[0] != frame.Events[0].Native.Value {
		t.Fatal("definition identity prevented equivalent native replay", err)
	}
	mapping := document.Mapping.Directions[p.EncodeEvent]
	mapping.Program.Then = &p.Expression{Op: "read", From: "input"}
	document.Mapping.Directions[p.EncodeEvent] = mapping
	modified, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	altered := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer altered.State.Release()
	if _, err := modified.EncodeFrame(t.Context(), frame, altered); err == nil {
		t.Fatal("native replay bypassed an incompatible authored mapping")
	}
	document, _ = from.Package()
	document.Spec.ID = "handwritten-metadata"
	event, err := p.EncodeValue(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.NativeEvent, ResponseID: frame.Events[0].ResponseID, Sequence: frame.Events[0].Sequence})
	if err != nil {
		t.Fatal(err)
	}
	mapping = document.Mapping.Directions[p.DecodeEvent]
	mapping.Program.Source.Otherwise.Then.Otherwise = &p.Expression{Op: "array", Items: []p.Expression{{Op: "literal", Value: event}}}
	document.Mapping.Directions[p.DecodeEvent] = mapping
	handwritten, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	decode := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer decode.State.Release()
	if _, err := handwritten.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), decode); err != nil {
		t.Fatal(err)
	}
	decoded, err := handwritten.DecodeFrame(t.Context(), frame.Events[0].Native.Value, decode)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != 1 || decoded.Events[0].Native == nil || decoded.Events[0].Native.Source.Path == "/metadata" {
		t.Fatal("handwritten mapping minted the auxiliary provenance marker", decoded)
	}
	to := testCompiled(t, Chat)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(to, handwritten))
	if err != nil {
		t.Fatal(err)
	}
	route := conversion.VerificationRoute(handwritten.Identity(), to.Identity(), p.SSE)
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Event(t.Context(), decoded.Events[0], route, sink)
	if err != nil || projected.Type != p.MetadataUpdated || projected.Native != nil || len(sink.Issues()) != 1 {
		t.Fatal("independently validated metadata lost its declared disposition", err, sink.Issues())
	}
	serialized, err := p.EncodeValue(decoded.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	var withoutReceipt p.Event
	if err := serialized.Decode(&withoutReceipt); err != nil {
		t.Fatal(err)
	}
	projected, err = conversion.Event(t.Context(), withoutReceipt, route, nil)
	if err == nil && (projected.Type != p.NativeEvent || projected.Native == nil) {
		t.Fatal("handwritten semantic metadata minted a source observation receipt")
	}
	document, _ = from.Package()
	document.Spec.ID = "bounded-metadata"
	limits := p.DefaultLimits()
	limits.BufferBytes = frameByteLimit
	document.Mapping.Limits = &limits
	bounded, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	boundedOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer boundedOptions.State.Release()
	if _, err := bounded.DecodeFrame(t.Context(), testValue(t, responsesPaddedTextFrames()[0]), boundedOptions); err != nil {
		t.Fatal(err)
	}
	large := testValue(t, `{"type":"response.metadata","response_id":"r","sequence_number":1,"metadata":{"payload":"`+strings.Repeat("x", limits.BufferBytes)+`"}}`)
	if _, err := bounded.DecodeFrame(t.Context(), large, boundedOptions); err == nil {
		t.Fatal("metadata bypassed the original frame budget")
	}
}

func TestChatResponseModelModuleSelectionAndCombination(t *testing.T) {
	document, _ := testCompiled(t, Chat).Package()
	document.Spec.ID = "old-chat-model-copy"
	custom, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(custom, custom)
	hasRule := false
	for _, rule := range policy.Rules {
		if rule.ID == "chat-response-model" {
			hasRule = rule.Action == "core_controls"
		}
	}
	if !hasRule {
		t.Fatal("custom package lost its declared response-model policy")
	}
	for _, direction := range []p.Direction{p.EncodeResponse, p.DecodeRequest} {
		modified, _ := custom.Package()
		mapping := modified.Mapping.Directions[direction]
		mapping.Program = p.Expression{Op: "read"}
		modified.Mapping.Directions[direction] = mapping
		invalid, issues := compilePackageDocument(t, modified)
		if invalid != nil || len(issues) == 0 || issues[0].Code != p.InvalidDefinition {
			t.Fatal("handwritten passthrough retained incompatible mapping declarations", direction, issues)
		}
	}
	for _, name := range []string{Responses, Anthropic, Gemini} {
		source := testCompiled(t, name)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(custom, source))
		if err != nil {
			t.Fatal(err)
		}
		response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Source: source.Identity(), ID: p.StringValue("r"), Status: p.StringValue("completed"), Content: []p.Node{}}
		sink := &p.DiagnosticSink{}
		projected, err := conversion.Response(t.Context(), response, p.ConversionContext{Source: source.Identity(), Target: custom.Identity(), Transport: p.HTTPJSON, Model: "routing-model"}, sink)
		if err == nil && !projected.Model.IsZero() {
			t.Fatal("foreign source inherited a Chat response-model repair", name, projected.Model)
		}
		for _, issue := range sink.Issues() {
			if issue.RuleID == "chat-response-model" {
				t.Fatal("foreign source was treated as a Chat model observation", name, issue)
			}
		}
	}
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{
		ID: "missing-model-audit", Direction: p.DecodeResponse, Input: october14Fixture(t, "chat_missing_model"),
		Context:  p.Object{"upstreamRequest": testValue(t, `{"model":"sent-model"}`)},
		Expected: testValue(t, `{"schemaVersion":2,"id":"chatcmpl-synthetic","status":"completed","attributes":{"finishReason":"stop","created_at":1700000000,"wire:openai_chat":{"vendor_integer":9007199254740993}},"content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"synthetic reply"}]}],"usage":{"input":{"count":7,"origin":"observed"},"output":{"count":3,"origin":"observed"},"total":{"count":10,"origin":"observed"}}}`),
	})
	fixture, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), fixture); !report.Passed {
		t.Fatal("independent missing-model fixture failed", report.Issues)
	}
	report := p.VerifyCombination(t.Context(), custom, fixture)
	hasSample, hasRepair := false, false
	for _, check := range report.Checks {
		if check.SampleID == "missing-model-audit" {
			hasSample = true
			if !check.Passed || check.Skipped {
				t.Fatal(check, report.Issues)
			}
		}
	}
	for _, issue := range report.Issues {
		hasRepair = hasRepair || issue.RuleID == "chat-response-model" && issue.Fidelity == "lossy_compatible"
	}
	if !hasSample || !hasRepair {
		t.Fatal("combination lost the original sample or its normalization evidence", report.Issues)
	}
}
