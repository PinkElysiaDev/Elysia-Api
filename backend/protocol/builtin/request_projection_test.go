package builtin

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicThinkingRetentionProjectionScope(t *testing.T) {
	source := testCompiled(t, Anthropic)
	document, _ := source.Package()
	document.Spec.ID, document.Spec.Family = "custom-history-copy", "custom-family"
	custom, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	const raw = `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`
	for _, sample := range []struct {
		name, target string
		source       *p.Compiled
		isDisabled   bool
	}{
		{"native", Anthropic, source, false}, {"chat", Chat, source, false}, {"responses", Responses, source, false},
		{"disabled", Gemini, source, true}, {"custom-package", Gemini, custom, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			target := testCompiled(t, sample.target)
			policy := p.DefaultConversionPolicy(sample.source, target)
			for index := range policy.Rules {
				if policy.Rules[index].ID == "request-projection" && sample.isDisabled {
					policy.Rules[index].Enabled = false
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			request, err := sample.source.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: sample.source.Identity(), Target: target.Identity()}, sink)
			var wire []byte
			if err == nil {
				wire, err = target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			}
			if sample.target == Chat || sample.target == Responses || sample.isDisabled {
				code := p.ConversionRejected
				if sample.isDisabled {
					code = p.UnsupportedCapability
				}
				requireContextIssue(t, err, code, "/reasoning/historyRetention")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sample.target == Anthropic {
				sameJSON(t, wire, raw)
				if len(sink.Issues()) != 0 {
					t.Fatal("native retention was projected", sink.Issues())
				}
			} else if !sinkHas(sink, "/reasoning/historyRetention") {
				t.Fatal("custom package normalization lacked history evidence", sink.Issues())
			}
		})
	}
}

func TestRequestProjectionCombinationReportsInfoLoss(t *testing.T) {
	report := verifyRequestEvidence(t, testCompiled(t, Responses), testCompiled(t, Gemini), "tracking-hint", `{"model":"m","input":"hi","store":false,"metadata":{"trace":"anonymous"}}`, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	if !report.Passed || report.Fidelity != "lossy_compatible" {
		t.Fatal(report.Fidelity, report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.RuleID == "request-projection" && issue.Severity == p.SeverityInfo && issue.Fidelity == "lossy_compatible" {
			return
		}
	}
	t.Fatal("info loss was omitted from combination evidence", report.Checks)
}

func TestRequestHintProjection(t *testing.T) {
	t.Parallel()
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				source, dest := testCompiled(t, Responses), testCompiled(t, target)
				req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hello","store":false,"prompt_cache_key":"session","prompt_cache_retention":"24h","metadata":{"trace":"opaque"},"user":"user-1"}`), fixtureOptions(p.EvaluationContext{}))
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
				wire, err := dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"prompt_cache_key", "prompt_cache_retention", "opaque"} {
					if strings.Contains(string(wire), field) != mapped {
						t.Fatalf("%s: %s", field, wire)
					}
				}
				if strings.Contains(string(wire), "user-1") != (mapped || target == Anthropic) {
					t.Fatal("canonical caller identity changed", string(wire))
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
				wantDrops := 4
				if target == Anthropic {
					wantDrops = 3
				}
				if !mapped && count != wantDrops {
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
	t.Parallel()
	raw := `{"model":"m","max_tokens":100,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"question","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"lookup","input":{"cache_control":{"type":"user-data"}},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"result","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"cache_control":{"type":"object"}}},"cache_control":{"type":"ephemeral","ttl":"1h"}}]}`
	source := testCompiled(t, Anthropic)
	req, err := source.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
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
				wire, err := dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
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
	t.Parallel()
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
		{"context-null", Anthropic, `"context_management":null`, false},
		{"context-empty", Anthropic, `"context_management":{"edits":[]}`, false},
		{"context-edit-type", Anthropic, `"context_management":{"edits":{}}`, false},
		{"context-keep-missing", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015"}]}`, false},
		{"context-keep-finite", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}}]}`, false},
		{"context-keep-extra", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`, false},
		{"context-edit-extra", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all","future":true}]}`, false},
		{"context-extra", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}],"future":true}`, false},
		{"context-mixed", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919"}]}`, false},
		{"context-compact", Anthropic, `"context_management":{"edits":[{"type":"compact_20260112"}]}`, false},
		{"context-sibling", Anthropic, `"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"future":{"n":9007199254740993}`, false},
		{"unknown-tool", Responses, `"tools":[{"type":"function","name":"f","parameters":{},"future":true}]`, false},
		{"strict-type", Responses, `"tools":[{"type":"function","name":"f","parameters":{},"strict":"true"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, dest := testCompiled(t, tc.codec), testCompiled(t, Gemini)
			history := `"input":"hello","store":false`
			if tc.codec == Anthropic {
				history = `"messages":[{"role":"user","content":"hello"}],"max_tokens":100`
			}
			req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m",`+history+`,`+tc.fields+`}`), fixtureOptions(p.EvaluationContext{}))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid hint accepted at ingress")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.EncodeRequest(t.Context(), req, fixtureOptions(p.EvaluationContext{})); err != nil {
				t.Fatal("native extension rejected", err)
			}
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
			out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
			if err == nil {
				_, err = dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
			}
			if err == nil {
				t.Fatal("unknown semantics disappeared")
			}
		})
	}
}

func TestAnthropicThinkingRetentionProjection(t *testing.T) {
	t.Parallel()
	source, target := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	const body = `{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"question"},{"role":"assistant","content":[{"type":"thinking","thinking":"first thought"},{"type":"text","text":"answer"}]},{"role":"user","content":"follow up"},{"role":"assistant","content":[{"type":"thinking","thinking":"second thought"},{"type":"text","text":"second answer"}]},{"role":"user","content":"continue"}]}`
	for _, mode := range []string{"compatible", "strict"} {
		t.Run(mode, func(t *testing.T) {
			policy := p.DefaultConversionPolicy(source, target)
			policy.Mode = mode
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
			baseline, err := source.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			baseline, err = conversion.Request(t.Context(), baseline, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			want, err := target.EncodeRequest(t.Context(), baseline, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			for _, keep := range []string{`"all"`, `{"type":"all"}`, `"a\u006cl"`} {
				raw := strings.TrimSuffix(body, "}") + `,"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":` + keep + `}]}}`
				request, err := source.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				before, _ := p.EncodeValue(request)
				sink := &p.DiagnosticSink{}
				out, err := conversion.Request(t.Context(), request, route, sink)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := target.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
				if err != nil || !bytes.Equal(wire, want) {
					t.Fatalf("retention changed Gemini history: %s vs %s: %v", wire, want, err)
				}
				issues := sink.Issues()
				if len(issues) != 1 {
					t.Fatal("expected one normalization", issues)
				}
				issue := issues[0]
				if issue.Code != p.ConversionNormalized || issue.Severity != p.SeverityInfo || issue.Fidelity != "preserved" || issue.RuleID != "request-projection" || issue.Path != "/reasoning/historyRetention" || issue.Direction != p.EncodeRequest || issue.Stage != "conversion.request" || issue.PolicyHash != conversion.Hash || issue.PolicyRevision == "" || issue.Evidence != "engine-default" {
					t.Fatal(issue)
				}
				after, _ := p.EncodeValue(request)
				if before != after {
					t.Fatal("source request changed")
				}
				wire, err = source.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, wire, raw)
			}
		})
	}
}

func TestRequestProjectionEmptyHintsAndToolStrict(t *testing.T) {
	t.Parallel()
	for _, fields := range []string{`"prompt_cache_key":null,"prompt_cache_retention":null,"metadata":{},"user":null`, `"metadata":null`} {
		source, dest := testCompiled(t, Responses), testCompiled(t, Gemini)
		req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi","store":false,`+fields+`}`), fixtureOptions(p.EvaluationContext{}))
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
		if _, err = dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{})); err != nil {
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
	t.Parallel()
	source, target := testCompiled(t, Chat), testCompiled(t, Gemini)
	req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"key","metadata":{"trace":"value"},"stream_options":{"include_usage":true}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Encode back to the source to exercise native reconciliation after omission.
	wire, err := source.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"prompt_cache_key", "metadata"} {
		if strings.Contains(string(wire), field) {
			t.Fatalf("native snapshot resurrected %s: %s", field, wire)
		}
	}
	if !strings.Contains(string(wire), `"stream_options":{"include_usage":true}`) {
		t.Fatal("caller delivery preference was lost with generation hints", string(wire))
	}
	upstream, err := target.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
	if err != nil || strings.Contains(string(upstream), "stream_options") {
		t.Fatal("source delivery options escaped into the target request", string(upstream), err)
	}
	native, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, source))
	out, err = native.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: source.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err = source.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
	if err != nil || !strings.Contains(string(wire), "prompt_cache_key") || !strings.Contains(string(wire), "metadata") {
		t.Fatal("previous candidate contaminated native candidate", string(wire), err)
	}
}

func TestInactiveStreamOptions(t *testing.T) {
	t.Parallel()
	for _, foreign := range []bool{false, true} {
		for _, raw := range []string{`null`, `{}`, `{"include_usage":true}`, `{"future":true}`, `{"include_usage":null}`} {
			source, target := testCompiled(t, Chat), testCompiled(t, Chat)
			if foreign {
				target = testCompiled(t, Gemini)
			}
			req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[],"store":false,"stream":false,"stream_options":`+raw+`}`), fixtureOptions(p.EvaluationContext{}))
			policy := p.DefaultConversionPolicy(source, target)
			policy.Mode = "strict"
			c, _ := p.ResolveConversion(policy)
			sink := &p.DiagnosticSink{}
			if err == nil {
				req, err = c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
			}
			var wire []byte
			if err == nil {
				wire, err = target.EncodeRequest(t.Context(), req, fixtureOptions(p.EvaluationContext{}))
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
	t.Parallel()
	source, dest := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":[{"type":"future","data":"opaque","cache_control":{"type":"ephemeral"}}]}],"tools":[{"type":"server-tool","name":"lookup","cache_control":{"type":"ephemeral"}}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
	out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := source.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "cache_control") || !strings.Contains(string(wire), "opaque") {
		t.Fatal("native snapshot restored removed hints", string(wire))
	}
	if _, err = dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{})); err == nil {
		t.Fatal("unmapped opaque content accepted")
	}
}

func TestRequestTrackingIdentityMapping(t *testing.T) {
	t.Parallel()
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		source, dest := testCompiled(t, Anthropic), testCompiled(t, codec)
		req, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"identity"}}`), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
		out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
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
			edited := req.Clone()
			edited.Parameters["user"] = p.StringValue("edited")
			projected, err := c.Request(t.Context(), edited, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := dest.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil || !strings.Contains(string(wire), `"user":"edited"`) || !req.Parameters["user"].IsString("identity") {
				t.Fatal("canonical identity edit was lost or changed its source", string(wire), err)
			}
		}
	}
}

func TestRequestTrackingIdentitySpellings(t *testing.T) {
	source := shippedProjectionProtocol(t, Anthropic)
	for _, codec := range []string{Chat, Responses} {
		target := shippedProjectionProtocol(t, codec)
		conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`"u"`, `"\u0075"`, `"<"`, `"\u003c"`, `null`, " \t\r\nnull \t", `""`, `1`, `1.0`} {
			t.Run(codec+"/"+raw, func(t *testing.T) {
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Source: source.Identity(), Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{"user": testValue(t, raw)}}
				input, err := p.EncodeValue(request)
				if err != nil {
					t.Fatal(err)
				}
				canEncode := request.Parameters["user"].IsNull() || request.Parameters["user"].IsStringValue()
				check := func(entry string, wire p.Value, err error) {
					t.Helper()
					if canEncode {
						if err != nil {
							t.Fatal(entry, err)
						}
						fields, err := wire.ReadObject()
						if err != nil {
							t.Fatal(err)
						}
						sameJSON(t, fields["user"].Bytes(), raw)
						return
					}
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) != 1 {
						t.Fatal(entry, "invalid canonical identity was not rejected", err)
					}
					issue := failure.Issues[0]
					isCoreRejection := issue.Code == p.InvalidInput && issue.Path == "/parameters/user"
					isWireRejection := issue.Code == p.ConversionRejected && issue.Path == "/user"
					if !isCoreRejection && !isWireRejection {
						t.Fatal(entry, "invalid canonical identity was not rejected", err)
					}
				}
				public, err := target.EncodeRequestValue(t.Context(), request.Clone(), fixtureOptions(p.EvaluationContext{}))
				check("public", public, err)
				wire, issues := target.Execute(t.Context(), p.EncodeRequest, input, fixtureOptions(p.EvaluationContext{}))
				check("raw", wire, p.IssuesError(issues))
				projected, err := conversion.Request(t.Context(), request.Clone(), p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, nil)
				var output p.Value
				if err == nil {
					output, err = target.EncodeRequestValue(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
				}
				check("projection", output, err)
			})
		}
	}
}

func TestRequestRetiredTrackingWrapperCannotOverrideIdentity(t *testing.T) {
	for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
		target := shippedProjectionProtocol(t, codec)
		for _, raw := range []string{`null`, `{}`, `{"user_id":"u"}`, `{"user_id":"\u0075"}`, `{"user_id":"other"}`, `{"user_id":null}`, `{"user_id":""}`, `{"user_id":1}`, `{"user_id":1.0}`, `{"user_id":"u","future":true}`} {
			input := testValue(t, `{"schemaVersion":2,"model":"m","content":[],"parameters":{"user":"u","anthropic_metadata":`+raw+`}}`)
			var request p.Request
			if err := input.Decode(&request); err != nil {
				t.Fatal(err)
			}
			_, err := target.EncodeRequestValue(t.Context(), &request, fixtureOptions(p.EvaluationContext{}))
			if err == nil {
				t.Fatal(codec, "retired wrapper was parsed or discarded", raw)
			}
			if _, issues := target.Execute(t.Context(), p.EncodeRequest, input, fixtureOptions(p.EvaluationContext{})); len(issues) == 0 {
				t.Fatal(codec, "raw entry accepted retired wrapper", raw)
			}
		}
	}
}

func TestRequestToolErrorAndUnknownMessageState(t *testing.T) {
	t.Parallel()
	source, dest := testCompiled(t, Anthropic), testCompiled(t, Chat)
	for _, value := range []string{`true`, `false`, `null`, `"true"`} {
		raw := `{"model":"m","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"result","is_error":` + value + `}]}]}`
		req, err := source.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
		if value == "null" || value == `"true"` {
			if err == nil {
				t.Fatal("invalid error state accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
		out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), "[Tool error]") != (value == "true") {
			t.Fatal(string(wire))
		}
		req.Content[1].Attributes = p.Object{"wire:claude": testValue(t, `{"future":true}`)}
		out, err = c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
		if err == nil {
			_, err = dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("unknown message state lost")
		}
	}
}

func TestRequestNativeExtensionsRequireCrossProtocolMapping(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"model":"m","messages":[{"role":"user","content":"hi","id":"vendor","status":{"future":true}}]}`,
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"prompt_cache_options":{"mode":"implicit","future":true}}`,
	} {
		source := testCompiled(t, Chat)
		req, err := source.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, codec := range []string{Chat, Responses, Anthropic, Gemini} {
			dest := testCompiled(t, codec)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, dest))
			out, err := c.Request(t.Context(), req, p.ConversionContext{Source: source.Identity(), Target: dest.Identity()}, nil)
			var wire []byte
			if err == nil {
				wire, err = dest.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
			}
			if codec != Chat {
				if err == nil {
					t.Fatal("unknown extension disappeared", codec)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, raw)
		}
	}
}
