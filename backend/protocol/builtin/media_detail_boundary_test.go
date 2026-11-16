package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

const (
	mediaDetailPayload     = `{"mime":"image/png","data":"YQ=="}`
	imagePreprocessingRule = "image-default-preprocessing"
)

func TestTargetImageDetailBoundary(t *testing.T) {
	for _, name := range []string{Anthropic, Gemini} {
		compiled := testCompiled(t, name)
		count, ok := compiled.TokenCountRequestProtocol("count_tokens")
		if !ok {
			t.Fatal("missing token count request mapping", name)
		}
		for _, detail := range []string{`"auto"`, `"high"`, `"low"`, `"original"`, `"future"`, `null`} {
			for _, isNested := range []bool{false, true} {
				placement := "message"
				if isNested {
					placement = "tool-result"
				}
				t.Run(name+"/"+detail+"/"+placement, func(t *testing.T) {
					request := newMediaDetailRequest(t, detail, isNested)
					path := "/content/0/children/0/payload/detail"
					if isNested {
						path = "/content/1/children/0/children/0/payload/detail"
					}
					for _, target := range []*p.Compiled{compiled, count} {
						_, err := target.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
						requireContextIssue(t, err, p.ConversionRejected, path)
					}
				})
			}
		}
	}
}

func TestImageDetailConversionPolicies(t *testing.T) {
	for _, sourceName := range []string{Chat, Responses} {
		for _, targetName := range []string{Anthropic, Gemini} {
			for _, mode := range []string{"compatible", "strict", "disabled", "rule-disabled"} {
				for _, detail := range []string{"", "auto", "high", "low"} {
					t.Run(sourceName+"/"+targetName+"/"+mode+"/"+detail, func(t *testing.T) {
						checkImageDetailPolicy(t, sourceName, targetName, mode, detail)
					})
				}
			}
		}
	}
}

func checkImageDetailPolicy(t *testing.T, sourceName, targetName, mode, detail string) {
	t.Helper()
	source, target := testCompiled(t, sourceName), testCompiled(t, targetName)
	request, err := source.DecodeRequest(t.Context(), []byte(buildMediaDetailInput(sourceName, detail)), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	request.Native = nil
	checkImageDetailOwnership(t, request)
	policy := p.DefaultConversionPolicy(source, target)
	if mode == "strict" {
		policy.Mode = mode
	}
	for index, rule := range policy.Rules {
		// Storage resolution is independent of media disposition.
		if (mode == "disabled" && rule.ID != "responses-storage") || (mode == "rule-disabled" && rule.ID == imagePreprocessingRule) {
			policy.Rules[index].Enabled = false
		}
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
	path := "/content/0/children/0/payload/detail"
	if mode == "strict" && detail == "auto" {
		var failure *p.ConversionError
		if !errors.As(err, &failure) {
			t.Fatal("strict accepted automatic preprocessing loss", err)
		}
		checkImageDetailIssue(t, failure.Issues, path, true)
		if len(sink.Issues()) != 0 {
			t.Fatal("strict preprocessing emitted a successful disposition", sink.Issues())
		}
		return
	}
	if err != nil {
		t.Fatal("media input failed before its target mapping", err)
	}
	isProjected := mode == "compatible" && detail == "auto"
	if isProjected {
		checkImageDetailIssue(t, sink.Issues(), path, false)
	} else if len(sink.Issues()) != 0 {
		t.Fatal("media handling introduced an implicit disposition", sink.Issues())
	}
	wire, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if detail != "" && !isProjected {
		requireContextIssue(t, err, p.ConversionRejected, path)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := target.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, decoded.Content[0].Children[0].Payload.Bytes(), mediaDetailPayload)
}

func checkImageDetailIssue(t *testing.T, issues []p.ConversionIssue, path string, isStrict bool) {
	t.Helper()
	if len(issues) != 1 {
		t.Fatal("expected one image preprocessing diagnostic", issues)
	}
	issue := issues[0]
	code, severity, fidelity := p.ConversionDegraded, p.SeverityWarning, "lossy_compatible"
	if isStrict {
		code, severity, fidelity = p.ConversionRejected, p.SeverityError, "rejected"
	}
	if issue.RuleID != imagePreprocessingRule || issue.Code != code || issue.Severity != severity || issue.Fidelity != fidelity || issue.Path != path || issue.Direction != p.EncodeRequest || issue.Stage != "conversion.request" || issue.PolicyHash == "" || issue.PolicyRevision == "" || !strings.Contains(issue.Reason, "source-specific resizing and token budgets are not guaranteed") {
		t.Fatal("image preprocessing diagnostic lost attribution or fidelity", issue)
	}
}

func checkImageDetailOwnership(t *testing.T, request *p.Request) {
	t.Helper()
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := p.EncodeValue(request)
		if err != nil || after != before {
			t.Error("image preprocessing mutated its source", err)
		}
	})
}

func TestAutomaticImageDetailProjectionOwnership(t *testing.T) {
	payloads := []string{
		`{"mime":"image/png","data":"YQ==","extra":9007199254740993,"nullable":null,"enabled":false,"nested":{"detail":"auto"}}`,
		`{"url":"https://example.test/image.png","mime":"image/png","extra":[null,false,9007199254740993]}`,
		`{"file":"file-owned","mime":"image/png","extra":{"kind":"image","payload":{"detail":"auto"},"n":9007199254740993}}`,
	}
	for _, targetName := range []string{Anthropic, Gemini} {
		for _, isNested := range []bool{false, true} {
			for index, payload := range payloads {
				t.Run(fmt.Sprintf("%s/%d/nested=%t", targetName, index, isNested), func(t *testing.T) {
					source, target := testCompiled(t, Chat), testCompiled(t, targetName)
					request := newMediaDetailRequest(t, `"auto"`, isNested)
					image, path := selectMediaDetailImage(request, isNested)
					image.Payload = testValue(t, strings.TrimSuffix(payload, "}")+`,"detail":"auto"}`)
					checkImageDetailOwnership(t, request)
					conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, target, imagePreprocessingRule, "compatible"))
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
					if err != nil {
						t.Fatal(err)
					}
					projectedImage, _ := selectMediaDetailImage(projected, isNested)
					sameJSON(t, projectedImage.Payload.Bytes(), payload)
					if !strings.Contains(projectedImage.Payload.JSONString(), "9007199254740993") {
						t.Fatal("image preprocessing lost integer precision", projectedImage.Payload)
					}
					checkImageDetailIssue(t, sink.Issues(), path, false)
				})
			}
		}
	}
}

func TestAutomaticImageDetailKeepsUnsupportedIntent(t *testing.T) {
	for _, targetName := range []string{Anthropic, Gemini} {
		for _, detail := range []string{`"high"`, `"low"`, `"original"`, `"future"`, `null`} {
			for _, isNested := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/nested=%t", targetName, detail, isNested), func(t *testing.T) {
					source, target := testCompiled(t, Chat), testCompiled(t, targetName)
					request := newMediaDetailRequest(t, detail, isNested)
					checkImageDetailOwnership(t, request)
					conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, target, imagePreprocessingRule, "compatible"))
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
					if err != nil || len(sink.Issues()) != 0 {
						t.Fatal("unsupported detail was treated as auto", sink.Issues(), err)
					}
					_, path := selectMediaDetailImage(projected, isNested)
					_, err = target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
					requireContextIssue(t, err, p.ConversionRejected, path)
				})
			}
		}
	}
}

func TestAutomaticImageDetailSameSourceReconstruction(t *testing.T) {
	for _, name := range []string{Chat, Responses} {
		t.Run(name, func(t *testing.T) {
			compiled := testCompiled(t, name)
			request, err := compiled.DecodeRequest(t.Context(), []byte(buildMediaDetailInput(name, "auto")), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			request.Content[0].Native = nil
			request.Content[0].Children[0].Native = nil
			wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := compiled.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, decoded.Content[0].Children[0].Payload.Bytes(), strings.TrimSuffix(mediaDetailPayload, "}")+`,"detail":"auto"}`)
		})
	}
}

func TestAutomaticImageDetailDefaultOperationBoundary(t *testing.T) {
	source := testCompiled(t, Chat)
	for _, name := range []string{Anthropic, Gemini} {
		t.Run(name, func(t *testing.T) {
			target := testCompiled(t, name)
			policy := declaredRulePolicy(t, source, target, imagePreprocessingRule, "compatible")
			if !policy.Rules[0].Enabled || policy.Rules[0].Phase != p.ConversionRequest {
				t.Fatal("image preprocessing is not an enabled request default", policy)
			}
			count, hasCount := target.TokenCountRequestProtocol("count_tokens")
			if !hasCount {
				t.Fatal("missing count mapping")
			}
			for _, rule := range p.DefaultConversionPolicy(source, count).Rules {
				if rule.ID == imagePreprocessingRule {
					t.Fatal("generation preprocessing leaked into token counting", rule)
				}
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
			if err != nil {
				t.Fatal(err)
			}
			request := newMediaDetailRequest(t, `"auto"`, true)
			checkImageDetailOwnership(t, request)
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
			if name == Gemini {
				requireContextIssue(t, err, p.ConversionRejected, "/content/1/children/0/children")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{})); err != nil {
				t.Fatal("supported nested image failed", err)
			}
		})
	}
}

func TestImageDetailGuardKeepsOpaqueToolValues(t *testing.T) {
	for _, name := range []string{Anthropic, Gemini} {
		for _, detail := range []string{"high", "auto"} {
			t.Run(name+"/"+detail, func(t *testing.T) {
				checkOpaqueImageDetail(t, name, detail)
			})
		}
	}
}

func checkOpaqueImageDetail(t *testing.T, name, detail string) {
	t.Helper()
	source, target := testCompiled(t, Chat), testCompiled(t, name)
	payload := testValue(t, `{"kind":"image","payload":{"detail":"`+detail+`"},"detail":null}`)
	result := payload
	if name == Anthropic {
		result = p.StringValue(payload.JSONString())
	}
	call := p.Node{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: payload}}
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "64")}, Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{call}},
		{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.ToolResultNode, Name: call.Name, CallID: call.CallID, Payload: result}}},
	}}
	checkImageDetailOwnership(t, request)
	conversion, err := p.ResolveConversion(declaredRulePolicy(t, source, target, imagePreprocessingRule, "compatible"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
	if err != nil || len(sink.Issues()) != 0 {
		t.Fatal("image preprocessing inspected an opaque tool value", sink.Issues(), err)
	}
	wire, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal("image guard inspected an opaque tool value", err)
	}
	decoded, err := target.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, decoded.Content[0].Children[0].Input.Value.Bytes(), payload.JSONString())
	sameJSON(t, decoded.Content[1].Children[0].Payload.Bytes(), result.JSONString())
}

func selectMediaDetailImage(request *p.Request, isNested bool) (*p.Node, string) {
	if isNested {
		return &request.Content[1].Children[0].Children[0], "/content/1/children/0/children/0/payload/detail"
	}
	return &request.Content[0].Children[0], "/content/0/children/0/payload/detail"
}

func newMediaDetailRequest(t *testing.T, detail string, isNested bool) *p.Request {
	t.Helper()
	image := p.Node{Kind: p.ImageNode, Payload: testValue(t, strings.TrimSuffix(mediaDetailPayload, "}")+`,"detail":`+detail+`}`)}
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Parameters: p.Object{"max_output_tokens": testValue(t, "64")}, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{image}}}}
	if isNested {
		call := p.Node{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{}`)}}
		result := p.Node{Kind: p.ToolResultNode, Name: call.Name, CallID: call.CallID, Children: []p.Node{image}}
		request.Content = []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{call}}, {Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{result}}}
	}
	return request
}

func buildMediaDetailInput(source, detail string) string {
	field := ""
	if detail != "" {
		field = `,"detail":"` + detail + `"`
	}
	if source == Chat {
		return `{"model":"m","max_completion_tokens":64,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YQ=="` + field + `}}]}]}`
	}
	return `{"model":"m","max_output_tokens":64,"store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,YQ=="` + field + `}]}]}`
}
