package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func requireContextIssue(t *testing.T, err error, code p.IssueCode, path string) {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) {
		t.Fatalf("expected %s at %s, got %v", code, path, err)
	}
	for _, issue := range failure.Issues {
		if issue.Code == code && issue.Path == path {
			return
		}
	}
	t.Fatalf("expected %s at %s, got %+v", code, path, failure.Issues)
}

func TestResponsesContextInstalledCodecs(t *testing.T) {
	ingress := shippedProjectionProtocol(t, Responses)
	for _, name := range []string{Responses, Gemini, Anthropic, Chat} {
		upstream := shippedProjectionProtocol(t, name)
		for _, mode := range []string{"compatible", "strict"} {
			for _, field := range []string{"", `,"previous_response_id":null`, `,"truncation":null`, `,"truncation":"auto"`, `,"truncation":"disabled"`} {
				t.Run(name+"/"+mode+"/"+field, func(t *testing.T) {
					raw := `{"model":"m","input":"hello","store":false,"max_output_tokens":8` + field + `}`
					req, err := ingress.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					policy := p.DefaultConversionPolicy(ingress, upstream)
					policy.Mode = mode
					c, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					out, err := c.Request(t.Context(), req, p.ConversionContext{Source: ingress.Identity(), Target: upstream.Identity()}, sink)
					if name != Responses && strings.Contains(field, "truncation") {
						requireContextIssue(t, err, p.ConversionRejected, "/parameters/contextTruncation")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wire, err := upstream.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					if name == Responses {
						sameJSON(t, wire, raw)
						if len(sink.Issues()) != 0 {
							t.Fatal("native path diagnosed a change", sink.Issues())
						}
					} else if strings.Contains(field, "previous_response_id") {
						if strings.Contains(string(wire), "previous_response") {
							t.Fatal(string(wire))
						}
						issues := sink.Issues()
						if len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].Severity != p.SeverityInfo || issues[0].Fidelity != "preserved" || issues[0].RuleID != "reasoning-controls" || issues[0].Path != "/parameters/wire:openai_responses/previous_response_id" || issues[0].PolicyHash != c.Hash {
							t.Fatal(issues)
						}
						owned, _ := req.Parameters["wire:openai_responses"].ReadObject()
						if !owned["previous_response_id"].IsNull() {
							t.Fatal("original request changed")
						}
					}
				})
			}
		}
	}
}

func TestResponsesContextTypesAndNativeBoundary(t *testing.T) {
	ingress := shippedProjectionProtocol(t, Responses)
	_, err := ingress.EncodeRequest(t.Context(), nil, fixtureOptions(p.EvaluationContext{}))
	requireContextIssue(t, err, p.InvalidInput, "/schemaVersion")
	for _, field := range []string{"truncation", "previous_response_id"} {
		for _, value := range []string{`42`, `true`, `[]`, `{}`, `""`} {
			_, err := ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","`+field+`":`+value+`}`), fixtureOptions(p.EvaluationContext{}))
			requireContextIssue(t, err, p.InvalidInput, "/"+field)
		}
	}
	_, err = ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","truncation":"future"}`), fixtureOptions(p.EvaluationContext{}))
	requireContextIssue(t, err, p.InvalidInput, "/truncation")
	_, err = ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","previous_response_id":"r"}`), fixtureOptions(p.EvaluationContext{ResolveRequestScope: func(*p.Request) (p.Scope, error) {
		t.Fatal("unsupported native reference reached routing")
		return p.Scope{}, nil
	}}))
	requireContextIssue(t, err, p.UnsupportedCapability, "/previous_response_id")
	if !strings.Contains(err.Error(), "verified previous-response context") {
		t.Fatal(err)
	}
}

func TestResponsesContextOldCustomAfterAndDisabled(t *testing.T) {
	document, _ := testCompiled(t, Responses).Package()
	document.Spec.ID, document.Spec.Family = "custom-response-copy", "user-family"
	mapping := document.Mapping.Directions[p.DecodeRequest]
	program := mapping.Program
	mapping.Program = p.Expression{Op: "let", Name: "decoded-request", Source: &program, Body: &p.Expression{Op: "read", From: "binding", Name: "decoded-request"}}
	document.Mapping.Directions[p.DecodeRequest] = mapping
	custom, issues := compilePackageDocument(t, document)
	if custom != nil || p.IssuesError(issues) == nil {
		t.Fatal("unproved identity wrapper bypassed the declared emit contract")
	}
	mapping.Program = program
	document.Mapping.Directions[p.DecodeRequest] = mapping
	custom, issues = compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	target := testCompiled(t, Gemini)
	request, err := custom.DecodeRequest(t.Context(), []byte(`{"input":"hi","store":false,"previous_response_id":null}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, isEnabled := range []bool{true, false} {
		policy := p.DefaultConversionPolicy(custom, target)
		for index := range policy.Rules {
			if policy.Rules[index].ID == "reasoning-controls" {
				policy.Rules[index].Enabled = isEnabled
			}
		}
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		output, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: custom.Identity(), Target: target.Identity()}, nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{}))
		}
		if (err == nil) != isEnabled {
			t.Fatal("custom source or disabled normalization crossed its boundary", isEnabled, err)
		}
		owned, _ := request.Parameters["wire:openai_responses"].ReadObject()
		if !owned["previous_response_id"].IsNull() {
			t.Fatal("normalization changed the original source intent")
		}
	}
}

func TestResponsesContextSemanticValidationAndEvidence(t *testing.T) {
	source, target := testCompiled(t, Responses), testCompiled(t, Gemini)
	policy := p.DefaultConversionPolicy(source, target)
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	for _, parameters := range []string{`{"contextTruncation":42}`, `{"wire:openai_responses":{"previous_response_id":42}}`, `{"wire:openai_responses":{"previous_response_id":"r"}}`} {
		request := &p.Request{SchemaVersion: p.ProtocolCoreVersion}
		if err := testValue(t, parameters).Decode(&request.Parameters); err != nil {
			t.Fatal(err)
		}
		output, err := conversion.Request(t.Context(), request, route, nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("unsupported context survived authored core input", parameters)
		}
	}
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}
	baseline := p.VerifyBindingCombination(t.Context(), source, target, contract)
	document, _ := source.Package()
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{ID: "null-reference-and-include", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"},
		Input:    testValue(t, `{"model":"m","input":"hello","store":false,"previous_response_id":null,"include":["reasoning.encrypted_content"]}`),
		Expected: testValue(t, `{"schemaVersion":2,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"parameters":{"wire:openai_responses":{"previous_response_id":null}},"clientOutput":{"requireContinuationState":true,"responsesStorage":{"sourceCodec":"responses","raw":false,"requested":false,"effective":false,"effectiveUnknown":true}}}`),
	})
	withSample, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), withSample); !report.Passed {
		t.Fatal("independent context expectation failed", report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), withSample, target, contract)
	if !baseline.Passed || !report.Passed || baseline.Fidelity != report.Fidelity {
		t.Fatal("no-effect context normalization changed combination fidelity", report.Issues)
	}
	hasNormalization := false
	for _, issue := range report.Issues {
		if issue.Path == "/parameters/wire:openai_responses/previous_response_id" && issue.Code == p.ConversionNormalized {
			if issue.RuleID != "reasoning-controls" || issue.Fidelity != "preserved" || issue.Severity != p.SeverityInfo {
				t.Fatal("normalization lost its source ownership", issue)
			}
			hasNormalization = true
		}
	}
	if !hasNormalization {
		t.Fatal("context evidence omitted its normalization diagnostic")
	}
}
