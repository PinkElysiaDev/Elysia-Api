package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAuditHTTPFailureDiagnosticUsesResponseStage(t *testing.T) {
	from := shippedProjectionProtocol(t, Chat)
	_, err := from.DecodeHTTPFailure(t.Context(), 524, []byte("{}"), p.EvaluationContext{})
	var conversion *p.ConversionError
	if !errors.As(err, &conversion) {
		t.Fatal(err)
	}
	for _, issue := range conversion.Issues {
		if issue.Direction != p.DecodeResponse || issue.Stage != "decode.response" || issue.Path != "/error" || issue.Protocol.DefinitionID != p.PresetChatCompletionsID {
			t.Fatal(issue)
		}
	}
}

func TestAuditFailureProjectionMatrix(t *testing.T) {
	for _, source := range []string{Chat, Responses} {
		from := shippedProjectionProtocol(t, source)
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			for _, fixture := range []string{"upstream_error", "rate_limit", "tool_choice_failure"} {
				for _, stream := range []bool{false, true} {
					conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
					sink := &p.DiagnosticSink{}
					raw := liveAuditFixture(t, fixture)
					var wire []byte
					if !stream {
						r, err := from.DecodeHTTPFailure(t.Context(), 400, raw.Bytes(), p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						r, err = conversion.Response(t.Context(), r, p.ConversionContext{}, sink)
						if err != nil {
							t.Fatal(source, target, fixture, err)
						}
						wire, err = to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
						if err != nil {
							t.Fatal(source, target, fixture, err)
						}
					} else {
						fields, _ := raw.ReadObject()
						if source == Responses {
							fields["type"] = p.StringValue("error")
						}
						frame, err := from.DecodeFrame(t.Context(), object(fields), p.EvaluationContext{State: p.NewEvaluationState()})
						if err != nil {
							t.Fatal(err)
						}
						opts := p.EvaluationContext{State: p.NewEvaluationState()}
						for _, event := range frame.Events {
							event, err = conversion.Event(t.Context(), event, p.ConversionContext{}, sink)
							if err != nil {
								t.Fatal(err)
							}
							frames, err := to.EncodeFrames(t.Context(), event, opts)
							if err != nil {
								t.Fatal(source, target, fixture, err)
							}
							for _, value := range frames {
								wire = append(wire, value.Bytes()...)
							}
						}
					}
					if !strings.Contains(string(wire), "Synthetic") && !strings.Contains(string(wire), "tool_choice.name") {
						t.Fatal(string(wire))
					}
					if fixture == "rate_limit" && !strings.Contains(string(wire), "gateway_concurrency_limit") {
						t.Fatal("lost detail", string(wire))
					}
					if source == target && len(sink.Issues()) != 0 {
						t.Fatal("native error degraded", sink.Issues())
					}
				}
			}
		}
	}
}

func TestErrorProjectionStrictDisabledAndUnknown(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	for _, mode := range []string{"compatible", "strict"} {
		for _, enabled := range []bool{false, true} {
			policy := p.DefaultConversionPolicy(to, from)
			policy.Mode = mode
			for i := range policy.Rules {
				if policy.Rules[i].Action == "error_metadata" {
					policy.Rules[i].Enabled = enabled
				}
			}
			conversion, _ := p.ResolveConversion(policy)
			r, err := from.DecodeHTTPFailure(t.Context(), 429, liveAuditFixture(t, "rate_limit").Bytes(), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := conversion.Response(t.Context(), r, p.ConversionContext{}, sink)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			}
			if (err == nil) != (enabled && mode == "compatible") {
				t.Fatal(mode, enabled, err)
			}
			if !strings.Contains(string(r.Error.Bytes()), "gateway_concurrency_limit") {
				t.Fatal("source error mutated")
			}
			if err == nil && len(sink.Issues()) == 0 {
				t.Fatal("missing detail projection diagnostic")
			}
		}
	}
	for _, extra := range []string{`"param":{}`, `"code":42`, `"vendor":{"secret":"synthetic"}`} {
		r, err := from.DecodeHTTPFailure(t.Context(), 400, []byte(`{"error":{"type":"invalid_request_error","message":"synthetic",`+extra+`}}`), p.EvaluationContext{})
		if err != nil {
			continue
		}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
		r, err = conversion.Response(t.Context(), r, p.ConversionContext{}, nil)
		if err == nil {
			_, err = to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("unknown error detail lost", extra)
		}
	}
}
