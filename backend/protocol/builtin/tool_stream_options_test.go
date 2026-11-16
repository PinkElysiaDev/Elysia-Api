package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestToolInputStreamingProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	to := shippedProjectionProtocol(t, Gemini)
	raw := `{"model":"m","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"lookup"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":true}]}`
	request, err := from.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
	projected, err := c.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "eager_input_streaming") || !strings.Contains(string(wire), "lookup") {
		t.Fatal("invalid tool projection", string(wire))
	}
}

func TestToolInputStreamingModesAndNativeOwnership(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	for _, target := range []string{Anthropic, Chat, Responses, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, stream := range []bool{false, true} {
			for _, value := range []string{"true", "false", "null"} {
				for _, mode := range []string{"compatible", "strict"} {
					t.Run(fmt.Sprintf("%s/%t/%s/%s", target, stream, value, mode), func(t *testing.T) {
						raw := fmt.Sprintf(`{"model":"m","max_tokens":100,"stream":%t,"messages":[{"role":"user","content":"lookup"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"eager_input_streaming":{"type":"boolean"}}},"eager_input_streaming":%s}]}`, stream, value)
						request, err := from.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
						if err != nil {
							t.Fatal(err)
						}
						before, _ := p.EncodeValue(request)
						policy := p.DefaultConversionPolicy(from, to)
						policy.Mode = mode
						c, _ := p.ResolveConversion(policy)
						sink := &p.DiagnosticSink{}
						projected, err := c.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
						loss := stream && target != Anthropic && value != "null"
						if loss && mode == "strict" {
							if err == nil || !strings.Contains(err.Error(), "eager_input_streaming") {
								t.Fatal("strict accepted streaming timing loss", err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						wire, err := to.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
						if err != nil {
							t.Fatal(err)
						}
						back, err := to.DecodeRequest(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
						if err != nil {
							t.Fatal(err)
						}
						kept := stream && target == Anthropic && value != "null"
						if !back.Tools[0].Options["eager_input_streaming"].IsZero() != kept || !equivalentJSON(back.Tools[0].InputSchema, request.Tools[0].InputSchema) {
							t.Fatal("stream preference or tool schema changed", string(wire))
						}
						found := false
						for _, issue := range sink.Issues() {
							if issue.Path != "/tools/0/options/eager_input_streaming" {
								continue
							}
							found = true
							severity := p.SeverityInfo
							if loss {
								severity = p.SeverityWarning
							}
							if issue.RuleID != "tool-options" || issue.Severity != severity || (issue.Code == p.ConversionDegraded) != loss || (issue.Fidelity == "lossy_compatible") != loss {
								t.Fatal(issue)
							}
						}
						if found == kept {
							t.Fatal("incorrect preference diagnostic", sink.Issues())
						}
						after, _ := p.EncodeValue(request)
						if before != after {
							t.Fatal("candidate mutated original request")
						}
					})
				}
			}
		}
	}
}

func TestToolInputStreamingValidationAndUnknownExtensions(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	for _, value := range []string{`1`, `"true"`, `[]`, `{}`} {
		raw := `{"model":"m","max_tokens":100,"messages":[],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":` + value + `}]}`
		if _, err := from.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{})); err == nil {
			t.Fatal("invalid streaming preference accepted", value)
		}
		request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Tools: []p.Tool{{Kind: p.FunctionTool, Options: p.Object{"eager_input_streaming": testValue(t, value)}}}}
		policy := p.DefaultConversionPolicy(from, to)
		policy.Rules = append(policy.Rules, p.ConversionRule{ID: "remove-before-project", Order: 1, Enabled: true, Phase: p.ConversionRequest, Action: "remove", Path: "/tools/0/options/eager_input_streaming"})
		c, _ := p.ResolveConversion(policy)
		if _, err := c.Request(t.Context(), request, fixtureConversionContext(t, c, p.ConversionRequest, request.Source), nil); err == nil {
			t.Fatal("authored removal hid invalid input", value)
		}
	}
	for _, enabled := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			raw := `{"model":"m","max_tokens":100,"stream":true,"messages":[],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":true}]}`
			if unknown {
				raw = strings.Replace(raw, `"eager_input_streaming":true`, `"eager_input_streaming":true,"future_streaming":true`, 1)
			}
			request, err := from.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			native, err := from.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, native, raw)
			policy := p.DefaultConversionPolicy(from, to)
			for i := range policy.Rules {
				if policy.Rules[i].ID == "tool-options" {
					policy.Rules[i].Enabled = enabled
				}
			}
			c, _ := p.ResolveConversion(policy)
			out, err := c.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
			if err == nil {
				_, err = to.EncodeRequest(t.Context(), out, fixtureOptions(p.EvaluationContext{}))
			}
			if (err == nil) != (enabled && !unknown) {
				t.Fatal("unknown extension or disabled projection bypassed guard", enabled, unknown, err)
			}
		}
	}
}
