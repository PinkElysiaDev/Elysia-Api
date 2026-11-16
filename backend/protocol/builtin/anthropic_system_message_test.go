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
			request, err := compiled.DecodeRequest(t.Context(), raw, fixtureOptions(p.EvaluationContext{}))
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
			wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
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
			wire, err = compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
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
		{"attributes", `,"output_config":{"effort":"medium"}`, "/content/1", "compatible", p.ConversionRejected},
		{"strict-scope", "", "/content/1", "strict", p.ConversionRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"question"},{"role":"system","content":"private instruction"` + test.extra + `}]}`
			request, err := from.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
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
			r, err := compiled.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
			// Reconciliation must apply an authored edit instead of returning
			// the original document just because the two codecs match.
			last := &r.Content[len(r.Content)-1]
			last.Children[0].Payload = p.StringValue("edited")
			wire, err = compiled.EncodeRequest(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
			if err != nil || !strings.Contains(string(wire), "edited") {
				t.Fatal("native edit lost", string(wire), err)
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
