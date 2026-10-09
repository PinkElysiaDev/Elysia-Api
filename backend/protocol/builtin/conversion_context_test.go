package builtin

import (
	"encoding/json"
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
					req, err := ingress.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
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
						requireContextIssue(t, err, p.ConversionRejected, "/truncation")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wire, err := upstream.EncodeRequest(t.Context(), out, p.EvaluationContext{})
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
						if len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].Severity != p.SeverityInfo || issues[0].Fidelity != "preserved" || issues[0].RuleID != "responses-context" || issues[0].PolicyHash != c.Hash {
							t.Fatal(issues)
						}
						if !req.Parameters["responses_previous_response_id"].IsNull() {
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
	_, err := ingress.EncodeRequest(t.Context(), nil, p.EvaluationContext{})
	requireContextIssue(t, err, p.InvalidInput, "/schemaVersion")
	for _, field := range []string{"truncation", "previous_response_id"} {
		for _, value := range []string{`42`, `true`, `[]`, `{}`, `""`} {
			_, err := ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","`+field+`":`+value+`}`), p.EvaluationContext{})
			requireContextIssue(t, err, p.InvalidInput, "/"+field)
		}
	}
	_, err = ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","truncation":"future"}`), p.EvaluationContext{})
	requireContextIssue(t, err, p.InvalidInput, "/truncation")
	_, err = ingress.DecodeRequest(t.Context(), []byte(`{"input":"hi","previous_response_id":"r"}`), p.EvaluationContext{ResolveRequestScope: func(*p.Request) (p.Scope, error) {
		t.Fatal("unsupported native reference reached routing")
		return p.Scope{}, nil
	}})
	requireContextIssue(t, err, p.UnsupportedCapability, "/previous_response_id")
	if !strings.Contains(err.Error(), "verified previous-response context") {
		t.Fatal(err)
	}
}

func TestResponsesContextOldCustomAfterAndDisabled(t *testing.T) {
	base := shippedProjectionProtocol(t, Responses).Definition()
	base.ID, base.Family, base.Requires = "old-response-copy", "user-family", nil
	mapping := base.Directions[p.DecodeRequest]
	mapping.After = &p.Expression{Op: "read", Path: ""}
	base.Directions[p.DecodeRequest] = mapping
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(base)
	custom, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	upstream := shippedProjectionProtocol(t, Gemini)
	req, err := custom.DecodeRequest(t.Context(), []byte(`{"input":"hi","store":false,"previous_response_id":null}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(custom, upstream)
	for _, enabled := range []bool{true, false} {
		for i := range policy.Rules {
			if policy.Rules[i].ID == "responses-context" {
				policy.Rules[i].Enabled = enabled
			}
		}
		c, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.Request(t.Context(), req, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = upstream.EncodeRequest(t.Context(), out, p.EvaluationContext{})
		if (err == nil) != enabled {
			t.Fatal(enabled, err)
		}
		if !enabled {
			requireContextIssue(t, err, p.UnsupportedCapability, "/parameters/responses_previous_response_id")
		}
	}
	// A post-mapping can mint standard parameters, so validate after it as well.
	mapping.After = &p.Expression{Op: "literal", Value: testValue(t, `{"schemaVersion":1,"source":{},"content":[],"parameters":{"responses_truncation":42}}`)}
	base.Directions[p.DecodeRequest] = mapping
	raw, _ = p.EncodeValue(base)
	custom, issues = compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	_, err = custom.DecodeRequest(t.Context(), []byte(`{"input":"hi"}`), p.EvaluationContext{})
	requireContextIssue(t, err, p.InvalidInput, "/truncation")

	// A handwritten encoder with the same family must not inherit deletions.
	base.Directions[p.EncodeRequest] = p.Mapping{Transform: &p.Expression{Op: "read", Path: ""}}
	raw, _ = p.EncodeValue(base)
	handwritten, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, r := range p.DefaultConversionPolicy(custom, handwritten).Rules {
		if r.Action == "responses_context" {
			t.Fatal("guessed encoder", r)
		}
	}
}

func TestResponsesContextSemanticValidationAndEvidence(t *testing.T) {
	ingress, target := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Gemini)
	baseline := p.VerifyBindingCombination(t.Context(), ingress, target, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	c, err := p.ResolveConversion(p.DefaultConversionPolicy(ingress, target))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, raw, path string
		code           p.IssueCode
	}{
		{"responses_truncation", `42`, "/truncation", p.InvalidInput},
		{"responses_previous_response_id", `42`, "/previous_response_id", p.InvalidInput},
		{"responses_previous_response_id", `"r"`, "/previous_response_id", p.ConversionRejected},
	} {
		value := testValue(t, `{"schemaVersion":1,"source":{},"content":[],"parameters":{"store":false,"`+tc.key+`":`+tc.raw+`}}`)
		_, err := c.ApplyValue(t.Context(), p.ConversionRequest, value, p.ConversionContext{}, nil)
		requireContextIssue(t, err, tc.code, tc.path)
	}
	definition := ingress.Definition()
	definition.Samples = append(definition.Samples, p.Sample{ID: "null-reference-and-include", Direction: p.DecodeRequest,
		Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"model":"m","input":"hello","store":false,"previous_response_id":null,"include":["reasoning.encrypted_content"]}`),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"parameters":{"store":false,"responses_previous_response_id":null,"responses_include":["reasoning.encrypted_content"]}}`),
	})
	raw, _ := json.Marshal(definition)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	ingress, issues := compiler.Compile(raw)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), ingress); !report.Passed {
		t.Fatal(report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), ingress, target, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	if report.Fidelity != baseline.Fidelity {
		t.Fatal("normalization changed combination fidelity", baseline.Fidelity, report.Fidelity)
	}
	found := map[string]bool{}
	for _, issue := range report.Issues {
		if issue.Code == p.ConversionNormalized {
			if issue.Fidelity != "preserved" || issue.Severity != p.SeverityInfo {
				t.Fatal(issue)
			}
			found[issue.RuleID] = true
		}
	}
	if !found["responses-include"] || !found["responses-context"] {
		t.Fatal(report.Issues)
	}
}
