package builtin

import (
	"errors"
	"os"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicSystemMessageClientRequest(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-code-system-message.json")
	if err != nil {
		t.Fatal(err)
	}
	compiled := shippedProjectionProtocol(t, Anthropic)
	for _, mode := range []string{"compatible", "strict"} {
		t.Run(mode, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), raw, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			policy := p.DefaultConversionPolicy(compiled, compiled)
			policy.Mode = mode
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: "claude-haiku-5-5"}, sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, string(raw))
			if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
				t.Fatal(err)
			}
			if len(sink.Issues()) != 0 {
				t.Fatal("lossless native request diagnosed as degradation", sink.Issues())
			}
			request.Model = p.StringValue("claude-haiku-5-5-routed")
			wire, err = compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, strings.Replace(string(raw), `"model": "claude-haiku-5-5"`, `"model": "claude-haiku-5-5-routed"`, 1))
		})
	}
}

func TestSystemHoistRequestDiagnostics(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	for _, test := range []struct {
		name, extra, path, mode string
		code                    p.IssueCode
	}{
		{"attributes", `,"output_config":{"effort":"medium"}`, "/messages/1/output_config", "compatible", p.UnsupportedCapability},
		{"strict-scope", "", "/messages/1", "strict", p.ConversionRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"question"},{"role":"system","content":"private instruction"` + test.extra + `}]}`
			request, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			policy := p.DefaultConversionPolicy(from, to)
			policy.Mode = test.mode
			conversion, _ := p.ResolveConversion(policy)
			_, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal(err)
			}
			issue := failure.Issues[0]
			if issue.Code != test.code || issue.Path != test.path || issue.Direction != p.EncodeRequest || issue.Stage != "conversion.request" || issue.Protocol != to.Identity() || issue.RuleID != "system-instruction-hoist" || issue.PolicyHash != conversion.Hash || issue.PolicyRevision == "" || issue.Evidence != "engine-default" || issue.Fidelity != "rejected" {
				t.Fatal("request diagnostic misclassified", issue)
			}
			value, _ := p.EncodeValue(issue)
			if strings.Contains(string(value.Bytes()), "private instruction") || strings.Contains(issue.Suggestion, "upstream stream") {
				t.Fatal("request diagnosis leaked payload or suggested a stream repair")
			}
		})
	}
}

func TestAnthropicSystemMessagePlacement(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Anthropic)
	for _, messages := range []string{
		`{"role":"system","content":"first"},{"role":"user","content":"question"}`,
		`{"role":"user","content":"question"},{"role":"system","content":"last"}`,
		`{"role":"system","content":"first"},{"role":"user","content":"question"},{"role":"system","content":[{"type":"text","text":"middle","cache_control":{"type":"ephemeral","ttl":"1h"}}],"output_config":{"effort":"medium"},"vendor":{"n":9007199254740993}},{"role":"assistant","content":"reply"},{"role":"system","content":"last"}`,
	} {
		for _, system := range []string{"", `,"system":[{"type":"text","text":"top","cache_control":{"type":"ephemeral"}}]`} {
			body := `{"model":"m","max_tokens":64,"messages":[` + messages + `]` + system + `}`
			r, err := compiled.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), r, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			// Reconciliation must apply an authored edit instead of returning
			// the original document just because the two codecs match.
			last := &r.Content[len(r.Content)-1]
			last.Children[0].Payload = p.StringValue("edited")
			wire, err = compiled.EncodeRequest(t.Context(), r, p.EvaluationContext{})
			if err != nil || !strings.Contains(string(wire), "edited") {
				t.Fatal("native edit lost", string(wire), err)
			}
		}
	}
}

func TestAnthropicSystemMessageCustomModulesAndExplicitHoist(t *testing.T) {
	base := shippedProjectionProtocol(t, Anthropic)
	body := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"system","content":"middle"}]}`
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	definition := base.Definition()
	definition.ID, definition.Requires = "old-anthropic-system-copy", nil
	for _, direction := range []p.Direction{p.DecodeRequest, p.EncodeRequest} {
		mapping := definition.Directions[direction]
		mapping.After = &p.Expression{Op: "read", Path: ""}
		definition.Directions[direction] = mapping
	}
	// Request compatibility must not depend on the codecs of other directions.
	mapping := definition.Directions[p.DecodeResponse]
	mapping.Module = Chat
	definition.Directions[p.DecodeResponse] = mapping
	raw, _ := p.EncodeValue(definition)
	custom, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*p.Compiled{{base, custom}, {custom, base}, {custom, custom}} {
		from, to := pair[0], pair[1]
		request, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		for _, shouldHoist := range []bool{false, true} {
			policy := p.DefaultConversionPolicy(from, to)
			if shouldHoist {
				policy.Rules = append(policy.Rules, p.ConversionRule{ID: "system-instruction-hoist", Phase: p.ConversionRequest, Order: 140, Enabled: true, Action: "system_instruction_hoist"})
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if shouldHoist {
				sameJSON(t, wire, `{"model":"m","max_tokens":64,"system":[{"type":"text","text":"middle"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
				if len(sink.Issues()) != 1 || sink.Issues()[0].Fidelity != "lossy_compatible" {
					t.Fatal("explicit scope change not diagnosed", sink.Issues())
				}
			} else {
				sameJSON(t, wire, body)
			}
		}
	}
}

func TestAnthropicSystemMessageFinalRoleContract(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Anthropic)
	for _, role := range []string{`"system"`, `"developer"`, `"tool"`, `"future"`, `null`, `42`, `{}`} {
		wire := testValue(t, `{"model":"m","max_tokens":64,"messages":[{"role":`+role+`,"content":"hi"}]}`)
		err := compiled.ValidateWireOutput(p.EncodeRequest, wire)
		if (err == nil) != (role == `"system"`) {
			t.Fatalf("request role %s: %v", role, err)
		}
	}
	response := testValue(t, `{"id":"r","model":"m","type":"message","role":"system","content":[],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}`)
	if err := compiled.ValidateWireOutput(p.EncodeResponse, response); err == nil {
		t.Fatal("request role change expanded response contract")
	}
}

func TestAnthropicSystemMessageCombinationAndAfterMapping(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Anthropic)
	body := `{"model":"m","max_tokens":64,"system":"top","messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"middle","cache_control":{"type":"ephemeral"}}],"output_config":{"effort":"medium"}}]}`
	request, err := compiled.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	definition := compiled.Definition()
	definition.ID = "system-message-evidence"
	expected, _ := p.EncodeValue(request)
	definition.Samples = append(definition.Samples, p.Sample{ID: "native-system-message", Direction: p.DecodeRequest, Input: testValue(t, body), Expected: expected, Capabilities: []p.Capability{p.TextCapability}})
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(definition)
	withSample, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), withSample); !report.Passed {
		t.Fatal(report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), withSample, compiled, compiled.Definition().Capabilities)
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	hasSystemEvidence := false
	for _, check := range report.Checks {
		hasSystemEvidence = hasSystemEvidence || (check.SampleID == "native-system-message" && check.Passed && !check.Skipped)
	}
	if !hasSystemEvidence {
		t.Fatal("system message sample omitted from binding evidence")
	}
	// A user mapping still runs, and the actual mapped wire is validated.
	definition.Native.Preserve = false
	mapping := definition.Directions[p.EncodeRequest]
	mapping.After = &p.Expression{Op: "merge", Policy: "last", Items: []p.Expression{
		{Op: "read", Path: ""},
		{Op: "literal", Value: testValue(t, `{"messages":[{"role":"developer","content":"bad role"}]}`)},
	}}
	definition.Directions[p.EncodeRequest] = mapping
	raw, _ = p.EncodeValue(definition)
	invalid, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	wire, err := invalid.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err == nil {
		err = invalid.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire)))
	}
	if err == nil {
		t.Fatal("invalid after mapping bypassed final contract")
	}
}
