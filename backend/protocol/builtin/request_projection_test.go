package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestRequestHintProjection(t *testing.T) {
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				source, dest := testCompiled(t, Responses), testCompiled(t, target)
				req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hello","store":false,"prompt_cache_key":"session","prompt_cache_retention":"24h","metadata":{"trace":"opaque"},"user":"user-1"}`), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				before, _ := p.EncodeValue(req)
				policy := p.DefaultConversionPolicy(source, dest)
				policy.Mode = mode
				c, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, sink)
				mapped := target == Chat || target == Responses
				if mode == "strict" && !mapped {
					if err == nil {
						t.Fatal("strict accepted lost hints")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wire, err := dest.EncodeRequest(t.Context(), out, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"prompt_cache_key", "prompt_cache_retention", "metadata", "user-1"} {
					if strings.Contains(string(wire), field) != mapped {
						t.Fatalf("%s: %s", field, wire)
					}
				}
				count := 0
				for _, issue := range sink.Issues() {
					if issue.RuleID != "request-projection" {
						continue
					}
					count++
					if issue.Code != p.ConversionDegraded || issue.Severity != p.SeverityInfo || issue.Fidelity != "lossy_compatible" {
						t.Fatal(issue)
					}
				}
				if !mapped && count != 4 {
					t.Fatal("missing hint diagnostics", sink.Issues())
				}
				after, _ := p.EncodeValue(req)
				if before != after {
					t.Fatal("source request mutated")
				}
			})
		}
	}
}

func TestRequestBreakpointsPreservePayloads(t *testing.T) {
	raw := `{"model":"m","max_tokens":100,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"question","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"lookup","input":{"cache_control":{"type":"user-data"}},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"result","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"cache_control":{"type":"object"}}},"cache_control":{"type":"ephemeral","ttl":"1h"}}]}`
	source := testCompiled(t, Anthropic)
	req, err := source.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				dest := testCompiled(t, target)
				policy := p.DefaultConversionPolicy(source, dest)
				policy.Mode = mode
				c, _ := p.ResolveConversion(policy)
				sink := &p.DiagnosticSink{}
				out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, sink)
				if mode == "strict" && target != Anthropic {
					if err == nil {
						t.Fatal("strict accepted cache loss")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wire, err := dest.EncodeRequest(t.Context(), out, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"system", "question", "result", "call-1", "user-data"} {
					if !strings.Contains(string(wire), field) {
						t.Fatalf("lost %s: %s", field, wire)
					}
				}
				if (target == Anthropic) != strings.Contains(string(wire), "ephemeral") {
					t.Fatalf("cache markers: %s", wire)
				}
				count := 0
				for _, issue := range sink.Issues() {
					if issue.RuleID == "request-projection" && issue.Path != "/tools/0/options/strict" {
						count++
						if issue.Severity != p.SeverityInfo || issue.Fidelity != "lossy_compatible" {
							t.Fatal(issue)
						}
					}
				}
				if target != Anthropic && count != 7 {
					t.Fatal("missing cache owner", count, sink.Issues())
				}
				issueCount := len(sink.Issues())
				if _, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, sink); err != nil {
					t.Fatal(err)
				}
				if len(sink.Issues()) != issueCount {
					t.Fatal("duplicate diagnostics", sink.Issues())
				}
			})
		}
	}
}

func TestRequestProjectionValidationAndUnknowns(t *testing.T) {
	for _, tc := range []struct {
		name, codec, fields string
		invalid             bool
	}{
		{"key-type", Responses, `"prompt_cache_key":42`, true},
		{"retention-type", Responses, `"prompt_cache_retention":{}`, true},
		{"metadata-type", Responses, `"metadata":[]`, true},
		{"metadata-value", Responses, `"metadata":{"tag":false}`, true},
		{"user-type", Responses, `"user":123`, true},
		{"metadata-user-type", Anthropic, `"metadata":{"user_id":123}`, true},
		{"marker-type", Anthropic, `"cache_control":{"type":42}`, true},
		{"marker-ttl", Anthropic, `"cache_control":{"type":"ephemeral","ttl":42}`, true},
		{"marker-missing-type", Anthropic, `"cache_control":{}`, true},
		{"unknown-marker", Anthropic, `"cache_control":{"type":"future"}`, false},
		{"unknown-marker-field", Anthropic, `"cache_control":{"type":"ephemeral","future":true}`, false},
		{"unknown-ttl", Anthropic, `"cache_control":{"type":"ephemeral","ttl":"2h"}`, false},
		{"unknown-retention", Responses, `"prompt_cache_retention":"future"`, false},
		{"unknown-tracking", Anthropic, `"metadata":{"user_id":"u","future":true}`, false},
		{"unknown-request", Responses, `"future":{"setting":true}`, false},
		{"unknown-tool", Responses, `"tools":[{"type":"function","name":"f","parameters":{},"future":true}]`, false},
		{"strict-type", Responses, `"tools":[{"type":"function","name":"f","parameters":{},"strict":"true"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, dest := testCompiled(t, tc.codec), testCompiled(t, Gemini)
			history := `"input":"hello","store":false`
			if tc.codec == Anthropic {
				history = `"messages":[{"role":"user","content":"hello"}],"max_tokens":100`
			}
			req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m",`+history+`,`+tc.fields+`}`), p.EvaluationContext{})
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid hint accepted at ingress")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.EncodeRequest(t.Context(), req, p.EvaluationContext{}); err != nil {
				t.Fatal("native extension rejected", err)
			}
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
			out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
			if err == nil {
				_, err = dest.EncodeRequest(t.Context(), out, p.EvaluationContext{})
			}
			if err == nil {
				t.Fatal("unknown semantics disappeared")
			}
		})
	}
}

func TestRequestProjectionEmptyHintsAndToolStrict(t *testing.T) {
	for _, fields := range []string{`"prompt_cache_key":null,"prompt_cache_retention":null,"metadata":{},"user":null`, `"metadata":null`} {
		source, dest := testCompiled(t, Responses), testCompiled(t, Gemini)
		req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi","store":false,`+fields+`}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		policy := p.DefaultConversionPolicy(source, dest)
		policy.Mode = "strict"
		c, _ := p.ResolveConversion(policy)
		sink := &p.DiagnosticSink{}
		out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = dest.EncodeRequest(t.Context(), out, p.EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
		for _, issue := range sink.Issues() {
			if issue.Severity != p.SeverityInfo || issue.Fidelity != "preserved" {
				t.Fatal(issue)
			}
		}
	}

}

func TestRequestProjectionNativeReplayAndCandidateIsolation(t *testing.T) {
	source, target := testCompiled(t, Chat), testCompiled(t, Gemini)
	req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"key","metadata":{"trace":"value"},"stream_options":{"include_usage":true}}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Encode back to the source to exercise native reconciliation after omission.
	wire, err := source.EncodeRequest(t.Context(), out, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"prompt_cache_key", "metadata", "stream_options"} {
		if strings.Contains(string(wire), field) {
			t.Fatalf("native snapshot resurrected %s: %s", field, wire)
		}
	}
	native, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, source))
	out, err = native.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: source.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err = source.EncodeRequest(t.Context(), out, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(wire), "prompt_cache_key") || !strings.Contains(string(wire), "metadata") {
		t.Fatal("previous candidate contaminated native candidate", string(wire), err)
	}
}

func TestInactiveStreamOptions(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		for _, raw := range []string{`null`, `{}`, `{"include_usage":true}`, `{"future":true}`, `{"include_usage":null}`} {
			source, target := testCompiled(t, Chat), testCompiled(t, Chat)
			if foreign {
				target = testCompiled(t, Gemini)
			}
			req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[],"store":false,"stream":false,"stream_options":`+raw+`}`), p.EvaluationContext{})
			policy := p.DefaultConversionPolicy(source, target)
			policy.Mode = "strict"
			c, _ := p.ResolveConversion(policy)
			sink := &p.DiagnosticSink{}
			if err == nil {
				req, err = c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
			}
			if strings.Contains(raw, "null}") || (foreign && strings.Contains(raw, "future")) {
				if err == nil {
					t.Fatal("invalid/unknown stream option removed", raw)
				}
				continue
			}
			if err != nil {
				t.Fatal(raw, err)
			}
			wire, err := target.EncodeRequest(t.Context(), req, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(raw, "future") != strings.Contains(string(wire), "future") {
				t.Fatal(string(wire))
			}
			for _, issue := range sink.Issues() {
				if issue.Code != p.ConversionNormalized || issue.Severity != p.SeverityInfo || issue.Fidelity != "preserved" {
					t.Fatal(issue)
				}
			}
		}
	}
}

func TestRequestProjectionNativeOpaqueCacheOwnership(t *testing.T) {
	source, dest := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":[{"type":"future","data":"opaque","cache_control":{"type":"ephemeral"}}]}],"tools":[{"type":"server-tool","name":"lookup","cache_control":{"type":"ephemeral"}}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
	out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := source.EncodeRequest(t.Context(), out, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "cache_control") || !strings.Contains(string(wire), "opaque") {
		t.Fatal("native snapshot restored removed hints", string(wire))
	}
	if _, err = dest.EncodeRequest(t.Context(), out, p.EvaluationContext{}); err == nil {
		t.Fatal("unmapped opaque content accepted")
	}
}

func TestRequestTrackingIdentityMapping(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		source, dest := testCompiled(t, Anthropic), testCompiled(t, codec)
		req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"identity"}}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
		out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := dest.EncodeRequest(t.Context(), out, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if (codec != Gemini) != strings.Contains(string(wire), "identity") {
			t.Fatal(string(wire))
		}
		if codec == Chat || codec == Responses {
			if !strings.Contains(string(wire), `"user":"identity"`) {
				t.Fatal(string(wire))
			}
			req.Parameters["user"] = p.StringValue("conflict")
			if _, err := c.Request(t.Context(), req, p.ConversionContext{}, nil); err == nil {
				t.Fatal("conflicting identity overwritten")
			}
		}
	}
}

func TestRequestProjectionCombinationReportsInfoLoss(t *testing.T) {
	source, dest := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Gemini)
	d := source.Definition()
	d.Samples = append(d.Samples, p.Sample{ID: "tracking-hint", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"model":"m","input":"hi","store":false,"metadata":{"trace":"anonymous"}}`),
		Expected: testValue(t, `{"schemaVersion":1,"source":{},"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hi"}]}],"parameters":{"store":false,"metadata":{"trace":"anonymous"}}}`)})
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(d)
	source, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), source); !report.Passed {
		t.Fatal(report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), source, dest, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	if !report.Passed || report.Fidelity != "lossy_compatible" {
		t.Fatal(report.Fidelity, report.Issues)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.RuleID == "request-projection" && issue.Severity == p.SeverityInfo && issue.Fidelity == "lossy_compatible" {
			found = true
		}
	}
	if !found {
		t.Fatal("info loss missing from combination evidence", report.Checks)
	}
}
